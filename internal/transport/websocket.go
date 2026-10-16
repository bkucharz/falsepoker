package transport

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bkucharz/falsepoker/internal/room"
	"github.com/gorilla/websocket"
)

const socketProtocol = "falsepoker.v1"
const outputCapacity = 16
const pingInterval = 20 * time.Second
const pongTimeout = 45 * time.Second
const writeTimeout = 10 * time.Second

// Internal timing values allow short real-network heartbeat tests without
// changing the public v1 operational defaults or using client clocks.
type socketTiming struct{ pingEvery, pongWait, writeWait time.Duration }

type outputFrame struct {
	kind int
	data []byte
}

type socket struct {
	server       *Server
	session      *session
	sub          *room.Subscription
	roomID       string
	conn         *websocket.Conn
	out          chan outputFrame
	done         chan struct{}
	once         sync.Once
	closeCode    int
	closeReason  string
	pingMu       sync.Mutex
	pendingPings []string
}

func (s *Server) upgrade(w http.ResponseWriter, r *http.Request, sess *session, code string) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 1 || len(query["room_id"]) != 1 || !opaqueID(query.Get("room_id")) {
		writeResponse(w, errorResponse("", invalidJSON()))
		return
	}
	supported := false
	for _, protocol := range websocket.Subprotocols(r) {
		if protocol == socketProtocol {
			supported = true
		}
	}
	if !supported {
		writeResponse(w, errorResponse("", fail(400, "UNSUPPORTED_PROTOCOL", "Use the falsepoker.v1 subprotocol.")))
		return
	}
	current, err := s.registry.Lookup(code)
	if err != nil {
		writeResponse(w, errorResponse("", roomFailure(err)))
		return
	}
	// Authenticate membership/lifetime before reporting operational capacity.
	membership, err := current.Membership(sess.player)
	if err != nil {
		writeResponse(w, errorResponse("", roomFailure(err)))
		return
	}
	if membership.RoomID != query.Get("room_id") {
		writeResponse(w, errorResponse("", roomFailure(room.ErrStaleRoom)))
		return
	}
	// Reserve process capacity before allocating per-connection queues. Pending
	// handshakes count too, and Close waits for them without touching an
	// incompletely initialized socket.
	s.socketMu.Lock()
	if s.closing.Load() || s.socketCount >= s.limits.MaxConnections {
		s.socketMu.Unlock()
		f := transient(429, "CONNECTION_LIMIT", "Connection capacity is unavailable.", 1)
		if s.closing.Load() {
			f = transient(503, "SHUTTING_DOWN", "The server is shutting down.", 1)
		}
		writeResponse(w, errorResponse("", f))
		return
	}
	s.socketCount++
	s.socketWG.Add(1)
	s.socketMu.Unlock()
	c := &socket{server: s, session: sess, roomID: membership.RoomID, out: make(chan outputFrame, outputCapacity), done: make(chan struct{})}
	defer func() {
		s.socketMu.Lock()
		delete(s.sockets, c)
		s.socketCount--
		s.socketMu.Unlock()
		s.socketWG.Done()
	}()
	c.sub, err = current.SubscribeLimited(sess.player, c.roomID, s.limits.MaxConnectionsPerMember)
	if err != nil {
		f := roomFailure(err)
		if errors.Is(err, room.ErrConnectionLimit) {
			f = transient(429, "CONNECTION_LIMIT", "Too many connections for this membership.", 1)
		}
		writeResponse(w, errorResponse("", f))
		return
	}
	defer c.sub.Close()
	s.socketMu.Lock()
	if s.closing.Load() {
		s.socketMu.Unlock()
		writeResponse(w, errorResponse("", transient(503, "SHUTTING_DOWN", "The server is shutting down.", 1)))
		return
	}
	s.sockets[c] = struct{}{}
	s.socketMu.Unlock()
	upgrader := websocket.Upgrader{
		Subprotocols: []string{socketProtocol}, HandshakeTimeout: writeTimeout,
		CheckOrigin: func(request *http.Request) bool {
			origins := request.Header.Values("Origin")
			return len(origins) == 1 && origins[0] == s.origin
		},
		Error: func(w http.ResponseWriter, _ *http.Request, status int, _ error) {
			writeResponse(w, errorResponse("", fail(status, "INVALID_REQUEST", "Invalid WebSocket upgrade.")))
		},
		EnableCompression: false,
	}
	select {
	case <-c.done:
		writeResponse(w, errorResponse("", transient(503, "SHUTTING_DOWN", "The server is shutting down.", 1)))
		return
	default:
	}
	c.conn, err = upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c.run()
}

func (c *socket) stop(code int, reason string) {
	c.once.Do(func() { c.closeCode, c.closeReason = code, reason; c.sub.Close(); close(c.done) })
}

func (c *socket) enqueue(frame outputFrame) {
	select {
	case <-c.done:
		return
	default:
	}
	select {
	case c.out <- frame:
	default:
		c.stop(4008, "Slow consumer")
	}
}

func (c *socket) send(value any) {
	data, err := json.Marshal(value)
	if err != nil || len(data) > maxOutputBytes {
		c.stop(1011, "Response encoding failed")
		return
	}
	c.enqueue(outputFrame{websocket.TextMessage, data})
}

func (c *socket) run() {
	defer c.conn.Close()
	c.conn.SetReadLimit(maxBodyBytes)
	if err := c.conn.SetReadDeadline(time.Now().Add(c.server.timing.pongWait)); err != nil {
		c.stop(1011, "Read deadline failed")
		return
	}
	// Handlers run in the reader. They enqueue control replies instead of
	// writing concurrently with snapshots, acknowledgments and heartbeats.
	c.conn.SetPingHandler(func(data string) error { c.enqueue(outputFrame{websocket.PongMessage, []byte(data)}); return nil })
	c.conn.SetPongHandler(func(data string) error { return c.pong(data) })
	c.conn.SetCloseHandler(func(code int, _ string) error { c.stop(code, "Connection closed"); return nil })
	written := make(chan struct{})
	go func() { defer close(written); c.writeLoop() }()
	c.readLoop()
	c.stop(1000, "Connection closed")
	<-written
}

func (c *socket) pong(data string) error {
	c.pingMu.Lock()
	matched := false
	for _, pending := range c.pendingPings {
		if data == pending {
			matched = true
			break
		}
	}
	if matched {
		c.pendingPings = nil
	}
	c.pingMu.Unlock()
	if matched {
		return c.conn.SetReadDeadline(time.Now().Add(c.server.timing.pongWait))
	}
	return nil
}

func (c *socket) subscriptionEnded() {
	switch {
	case errors.Is(c.sub.Err(), room.ErrSlowSubscriber):
		c.stop(4008, "Slow consumer")
	case errors.Is(c.sub.Err(), room.ErrRoomClosed):
		c.stop(4004, "Room unavailable")
	default:
		c.stop(4003, "Membership ended")
	}
}

func (c *socket) writeFrame(frame outputFrame) bool {
	if err := c.conn.SetWriteDeadline(time.Now().Add(c.server.timing.writeWait)); err != nil {
		return false
	}
	return c.conn.WriteMessage(frame.kind, frame.data) == nil
}

func (c *socket) writeSnapshot(view room.RoomView) bool {
	data, err := json.Marshal(struct {
		Type  string    `json:"type"`
		State roomState `json:"state"`
	}{"snapshot", toRoomState(view)})
	if err != nil || len(data) > maxOutputBytes {
		c.stop(1011, "Response encoding failed")
		return false
	}
	return c.writeFrame(outputFrame{websocket.TextMessage, data})
}

// Only this loop writes application/control output. Gorilla also serializes
// its own protocol-error control replies internally (e.g. an invalid frame).
func (c *socket) writeLoop() {
	defer c.conn.Close() // interrupts a blocked reader, including on shutdown
	defer func() {
		select {
		case <-c.done:
			_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(c.closeCode, c.closeReason), time.Now().Add(c.server.timing.writeWait))
		default:
		}
	}()
	// Acknowledgments can never overtake the initial snapshot.
	select {
	case <-c.done:
		return
	case <-c.sub.Done():
		c.subscriptionEnded()
		return
	case initial, ok := <-c.sub.Updates():
		if !ok {
			c.subscriptionEnded()
			return
		}
		if !c.writeSnapshot(initial) {
			return
		}
	}
	ticker := time.NewTicker(c.server.timing.pingEvery)
	defer ticker.Stop()
	sequence := uint64(0)
	for {
		// Give termination priority over already queued output.
		select {
		case <-c.done:
			return
		case <-c.sub.Done():
			c.subscriptionEnded()
			return
		default:
		}
		select {
		case <-c.done:
			return
		case <-c.sub.Done():
			c.subscriptionEnded()
			return
		case view, ok := <-c.sub.Updates():
			if !ok {
				c.subscriptionEnded()
				return
			}
			select {
			case <-c.sub.Done():
				c.subscriptionEnded()
				return
			default:
			}
			if !c.writeSnapshot(view) {
				return
			}
		case frame := <-c.out:
			if !c.writeFrame(frame) {
				return
			}
		case <-ticker.C:
			sequence++
			nonce := strconv.FormatUint(sequence, 16)
			c.pingMu.Lock()
			c.pendingPings = append(c.pendingPings, nonce)
			if len(c.pendingPings) > 3 {
				c.pendingPings = c.pendingPings[1:]
			}
			c.pingMu.Unlock()
			if !c.writeFrame(outputFrame{websocket.PingMessage, []byte(nonce)}) {
				return
			}
		}
	}
}

type socketOutcome struct {
	Type         string   `json:"type"`
	RequestID    *string  `json:"request_id"`
	RoomID       string   `json:"room_id"`
	Revision     string   `json:"revision"`
	Changed      *bool    `json:"changed,omitempty"`
	Replayed     bool     `json:"replayed"`
	Error        *problem `json:"error,omitempty"`
	RetryAfterMS int64    `json:"retry_after_ms,omitempty"`
}

func (c *socket) errorMessage(id string, revision uint64, f *failure, replayed bool, delay time.Duration) {
	var requestID *string
	if id != "" {
		requestID = &id
	}
	retryMS := int64(0)
	if delay > 0 {
		retryMS = max(1, int64((delay+time.Millisecond-1)/time.Millisecond))
	}
	c.send(socketOutcome{Type: "error", RequestID: requestID, RoomID: c.roomID, Revision: counter(revision), Replayed: replayed, Error: &problem{f.code, f.message, delay > 0}, RetryAfterMS: retryMS})
}

func (c *socket) readLoop() {
	malformed, limited := 0, 0
	for {
		kind, data, err := c.conn.ReadMessage()
		if err != nil {
			if errors.Is(err, websocket.ErrReadLimit) {
				c.stop(1009, "Message too large")
			}
			return
		}
		if kind != websocket.TextMessage {
			c.stop(1003, "Use text messages")
			return
		}
		if !utf8.Valid(data) {
			c.stop(1007, "Invalid UTF-8")
			return
		}
		sess := c.session
		// Preserve the established session -> room lock order, including concurrent
		// HTTP leave. No socket I/O happens while either lock is held.
		sess.mu.Lock()
		if c.server.closing.Load() {
			sess.mu.Unlock()
			c.stop(1001, "Server shutdown")
			return
		}
		if !sess.active {
			sess.mu.Unlock()
			c.stop(4001, "Session unavailable")
			return
		}
		now := c.server.now()
		sess.lastSeen = now
		allowed, delay := sess.mutations.allow(now, c.server.limits.MutationRate, c.server.limits.MutationBurst)
		if !allowed {
			sess.mu.Unlock()
			limited++
			command, _ := parseCommand(data)
			c.reportInputError(command.RequestID, transient(429, "RATE_LIMITED", "Too many mutations.", delay))
			if limited >= 3 {
				c.stop(1008, "Repeated rate limit violations")
				return
			}
			continue
		}
		limited = 0
		command, f := parseCommand(data)
		if f != nil {
			sess.mu.Unlock()
			malformed++
			c.reportInputError(command.RequestID, f)
			if malformed >= 3 {
				c.stop(1008, "Repeated malformed messages")
				return
			}
			continue
		}
		malformed = 0
		result := c.sub.Execute(command, now)
		sess.mu.Unlock()
		if errors.Is(result.Err, room.ErrSubscriptionClosed) {
			c.subscriptionEnded()
			return
		}
		if result.Err != nil {
			f := commandFailure(result.Err)
			if errors.Is(result.Err, room.ErrRetryWindowFull) {
				f = fail(429, "RETRY_WINDOW_FULL", "Retry storage is full.")
			}
			c.errorMessage(command.RequestID, result.Revision, f, result.Replayed, result.RetryAfter)
		} else {
			c.send(socketOutcome{Type: "ack", RequestID: &command.RequestID, RoomID: c.roomID, Revision: counter(result.Revision), Changed: &result.Changed, Replayed: result.Replayed})
		}
	}
}

func (c *socket) reportInputError(id string, f *failure) {
	revision, err := c.sub.Revision()
	if err != nil {
		c.subscriptionEnded()
		return
	}
	c.errorMessage(id, revision, f, false, time.Duration(f.delay)*time.Second)
}
