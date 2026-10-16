package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkucharz/falsepoker/internal/room"
	"github.com/bkucharz/falsepoker/internal/testclock"
	"github.com/bkucharz/falsepoker/internal/transport"
	"github.com/gorilla/websocket"
)

type clock struct{ *testclock.Clock }

func (c clock) AfterFunc(d time.Duration, fn func()) room.Timer { return c.Clock.AfterFunc(d, fn) }

type harness struct {
	t           *testing.T
	server      *httptest.Server
	app         *transport.Server
	clock       clock
	sequence    atomic.Uint64
	mu          sync.Mutex
	secrets     map[string]bool
	connections []*connection
}
type actor struct {
	h        *harness
	id, name string
	http     *http.Client
}
type event struct {
	message message
	err     error
}
type connection struct {
	actor      *actor
	membership membership
	ws         *websocket.Conn
	state      snapshot // only the test goroutine owns rendered state
	events     chan event
	readDone   chan struct{}
	closed     atomic.Bool
	mu         sync.Mutex
	pending    map[string]int
	drop       map[string]bool
	dropped    chan message
	snapshots  atomic.Uint64
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	c := clock{testclock.New()}
	registry, err := room.NewRegistryWithSeedSource(func() (int64, error) { return 42, nil })
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	app, err := transport.NewServer(transport.Config{Origin: "http://" + server.Listener.Addr().String(), AllowInsecureCookies: true, Registry: registry, Clock: c, Limits: transport.Limits{MutationRate: 10000, MutationBurst: 10000, BootstrapRate: 10000, BootstrapBurst: 10000}})
	if err != nil {
		server.Close()
		registry.Close()
		t.Fatal(err)
	}
	server.Config.Handler = app
	server.Start()
	h := &harness{t: t, server: server, app: app, clock: c, secrets: make(map[string]bool)}
	t.Cleanup(func() {
		h.mu.Lock()
		connections := append([]*connection(nil), h.connections...)
		h.mu.Unlock()
		for _, conn := range connections {
			conn.closed.Store(true)
			_ = conn.ws.Close()
		}
		for _, conn := range connections {
			select {
			case <-conn.readDone:
			case <-time.After(5 * time.Second):
				t.Error("headless reader did not stop")
			}
		}
		app.Close()
		server.Close()
		if c.Pending() != 0 {
			t.Error("network fixture retained membership timers")
		}
	})
	return h
}
func (h *harness) key() string { return fmt.Sprintf("%032x", h.sequence.Add(1)) }
func (h *harness) inspect(data []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for secret := range h.secrets {
		if bytes.Contains(data, []byte(secret)) {
			return fmt.Errorf("session credential exposed in network payload")
		}
	}
	return nil
}
func (h *harness) request(a *actor, method, path string, data any, key string, status int, target any) {
	h.t.Helper()
	var body io.Reader
	if data != nil {
		encoded, err := json.Marshal(data)
		if err != nil {
			h.t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	r, err := http.NewRequest(method, h.server.URL+path, body)
	if err != nil {
		h.t.Fatal(err)
	}
	if method == "POST" {
		r.Header.Set("Origin", h.server.URL)
		r.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	response, err := a.http.Do(r)
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close()
	dataBytes, err := io.ReadAll(io.LimitReader(response.Body, 64<<10+1))
	if err != nil || len(dataBytes) > 64<<10 {
		h.t.Fatal("invalid HTTP response size/read")
	}
	u, _ := url.Parse(h.server.URL)
	h.mu.Lock()
	for _, cookie := range a.http.Jar.Cookies(u) {
		if cookie.Name == transport.CookieName {
			h.secrets[cookie.Value] = true
		}
	}
	h.mu.Unlock()
	if err := h.inspect(dataBytes); err != nil {
		h.t.Fatal(err)
	}
	if response.StatusCode != status || response.Header.Get("Content-Type") != "application/json" || response.Header.Get("Cache-Control") != "no-store" {
		h.t.Fatalf("%s %s: status %d, expected %d", method, path, response.StatusCode, status)
	}
	if target != nil {
		if err := decode(dataBytes, target); err != nil {
			h.t.Fatalf("HTTP protocol: %v", err)
		}
	}
}
func (h *harness) actor(name string) *actor {
	h.t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		h.t.Fatal(err)
	}
	a := &actor{h: h, name: name, http: &http.Client{Jar: jar, Timeout: 5 * time.Second}}
	var session struct {
		ProtocolVersion int    `json:"protocol_version"`
		PlayerID        string `json:"player_id"`
	}
	h.request(a, "POST", "/api/v1/session", struct{}{}, "", 201, &session)
	if session.ProtocolVersion != 1 || session.PlayerID == "" {
		h.t.Fatal("invalid anonymous session")
	}
	a.id = session.PlayerID
	return a
}
func (a *actor) create() membership {
	a.h.t.Helper()
	var m membership
	a.h.request(a, "POST", "/api/v1/rooms", map[string]string{"display_name": a.name}, a.h.key(), 201, &m)
	if m.PlayerID != a.id || m.Revision != "1" || m.RoomID == "" || len(m.RoomCode) != 12 {
		a.h.t.Fatal("invalid creation metadata")
	}
	return m
}
func (a *actor) join(m membership) {
	a.h.t.Helper()
	var joined membership
	a.h.request(a, "POST", "/api/v1/rooms/"+m.RoomCode+"/join", map[string]string{"display_name": a.name}, a.h.key(), 201, &joined)
	if joined.PlayerID != a.id || joined.RoomID != m.RoomID || joined.RoomCode != m.RoomCode {
		a.h.t.Fatal("join selected wrong membership")
	}
}
func (a *actor) view(m membership) snapshot {
	a.h.t.Helper()
	var response struct {
		State snapshot `json:"state"`
	}
	a.h.request(a, "GET", "/api/v1/rooms/"+m.RoomCode, nil, "", 200, &response)
	if err := validateSnapshot(response.State, a.id, m); err != nil {
		a.h.t.Fatal(err)
	}
	return response.State
}
func (a *actor) connect(m membership) *connection {
	a.h.t.Helper()
	u, _ := url.Parse(a.h.server.URL)
	headers := http.Header{"Origin": []string{a.h.server.URL}}
	for _, cookie := range a.http.Jar.Cookies(u) {
		headers.Add("Cookie", cookie.String())
	}
	dialer := websocket.Dialer{Subprotocols: []string{"falsepoker.v1"}, HandshakeTimeout: 5 * time.Second}
	ws, response, err := dialer.Dial(strings.Replace(a.h.server.URL, "http", "ws", 1)+"/api/v1/rooms/"+m.RoomCode+"/ws?room_id="+url.QueryEscape(m.RoomID), headers)
	if err != nil {
		if response != nil {
			response.Body.Close()
		}
		a.h.t.Fatal(err)
	}
	if ws.Subprotocol() != "falsepoker.v1" {
		ws.Close()
		a.h.t.Fatal("subprotocol not negotiated")
	}
	ws.SetReadLimit(64 << 10)
	c := &connection{actor: a, membership: m, ws: ws, events: make(chan event, 64), readDone: make(chan struct{}), pending: make(map[string]int), drop: make(map[string]bool), dropped: make(chan message, 1)}
	a.h.mu.Lock()
	a.h.connections = append(a.h.connections, c)
	a.h.mu.Unlock()
	go c.readLoop()
	first := c.next()
	if first.Type != "snapshot" {
		a.h.t.Fatal("initial WebSocket message was not a snapshot")
	}
	return c
}

func (c *connection) emit(e event) bool {
	select {
	case c.events <- e:
		return true
	default:
		c.actor.h.t.Error("headless client output queue overflow")
		return false
	}
}
func (c *connection) readLoop() {
	defer close(c.readDone)
	previous := uint64(0)
	for {
		kind, data, err := c.ws.ReadMessage()
		if err != nil {
			if !c.closed.Load() && !websocket.IsCloseError(err, 1000) {
				c.actor.h.t.Errorf("%s unexpectedly disconnected: %v", c.actor.name, err)
			}
			c.emit(event{err: err})
			return
		}
		var m message
		if kind != websocket.TextMessage {
			err = fmt.Errorf("non-text server application message")
		} else if err = c.actor.h.inspect(data); err == nil {
			err = decode(data, &m)
		}
		if err == nil {
			switch m.Type {
			case "snapshot":
				if m.State == nil || m.RequestID != nil || m.Error != nil || m.Changed != nil {
					err = fmt.Errorf("invalid snapshot envelope")
					break
				}
				err = validateSnapshot(*m.State, c.actor.id, c.membership)
				if err == nil {
					revision, _ := number(m.State.Revision)
					if revision <= previous {
						err = fmt.Errorf("out-of-order snapshot")
					}
					previous = revision
					c.snapshots.Add(1)
				}
			case "ack", "error":
				if m.RoomID != c.membership.RoomID || m.State != nil {
					err = fmt.Errorf("outcome leaked room/state")
					break
				}
				if _, err = number(m.Revision); err != nil {
					break
				}
				if m.Type == "ack" && (m.RequestID == nil || m.Changed == nil || m.Error != nil) {
					err = fmt.Errorf("invalid ack")
					break
				}
				if m.Type == "error" && (m.Error == nil || m.Changed != nil) {
					err = fmt.Errorf("invalid error")
					break
				}
				if m.RequestID == nil {
					err = fmt.Errorf("uncorrelated outcome in valid-client scenario")
					break
				}
				c.mu.Lock()
				count := c.pending[*m.RequestID]
				if count > 0 {
					c.pending[*m.RequestID]--
				}
				drop := c.drop[*m.RequestID]
				delete(c.drop, *m.RequestID)
				c.mu.Unlock()
				if count == 0 {
					err = fmt.Errorf("outcome delivered to a connection that did not send the request")
					break
				}
				if drop {
					select {
					case c.dropped <- m:
					default:
						c.actor.h.t.Error("outcome-loss filter queue overflow")
						return
					}
					continue
				} // intentionally lose only an inspected, correlated outcome
			default:
				err = fmt.Errorf("unknown server message type")
			}
		}
		if err != nil {
			c.actor.h.t.Errorf("%s protocol/privacy violation: %v", c.actor.name, err)
			c.emit(event{err: err})
			return
		}
		if !c.emit(event{message: m}) {
			return
		}
	}
}
func (c *connection) next() message {
	c.actor.h.t.Helper()
	select {
	case event := <-c.events:
		if event.err != nil {
			c.actor.h.t.Fatalf("%s socket: %v", c.actor.name, event.err)
		}
		if event.message.Type == "snapshot" {
			c.state = *event.message.State
		}
		return event.message
	case <-time.After(5 * time.Second):
		c.actor.h.t.Fatalf("timed out waiting for %s socket", c.actor.name)
		return message{}
	}
}
func (c *connection) sync(revision string) {
	c.actor.h.t.Helper()
	for c.state.Revision != revision {
		current, _ := number(c.state.Revision)
		target, _ := number(revision)
		if current > target {
			c.actor.h.t.Fatal("room advanced more than expected")
		}
		if m := c.next(); m.Type != "snapshot" {
			c.actor.h.t.Fatal("unexpected outcome while synchronizing another connection")
		}
	}
}
func (c *connection) command(action string, data any) command {
	round := "0"
	if c.state.Game != nil {
		round = c.state.Game.RoundID
	}
	return command{"command", c.actor.h.key(), c.state.RoomID, c.state.Revision, c.state.GameID, round, action, data}
}
func (c *connection) send(cmd command) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.pending[cmd.RequestID]++
	c.mu.Unlock()
	if err := c.ws.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	return c.ws.WriteMessage(websocket.TextMessage, data)
}
func (c *connection) outcome(id string) message {
	c.actor.h.t.Helper()
	for {
		m := c.next()
		if m.Type != "snapshot" {
			if m.RequestID == nil || *m.RequestID != id {
				c.actor.h.t.Fatal("wrong request outcome")
			}
			return m
		}
	}
}
func (c *connection) apply(cmd command) message {
	c.actor.h.t.Helper()
	if err := c.send(cmd); err != nil {
		c.actor.h.t.Fatal(err)
	}
	return c.outcome(cmd.RequestID)
}
func (c *connection) disconnect() {
	c.actor.h.t.Helper()
	if err := c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1000, "disconnect"), time.Now().Add(5*time.Second)); err != nil {
		c.actor.h.t.Fatal(err)
	}
	for {
		select {
		case e := <-c.events:
			if e.err != nil {
				if !websocket.IsCloseError(e.err, 1000) {
					c.actor.h.t.Fatalf("disconnect: %v", e.err)
				}
				c.closed.Store(true)
				return
			}
		case <-time.After(5 * time.Second):
			c.actor.h.t.Fatal("disconnect timeout")
		}
	}
}

type group struct {
	h           *harness
	m           membership
	observer    *connection
	connections []*connection
}

func (h *harness) group(names ...string) *group {
	h.t.Helper()
	admin := h.actor("Admin")
	m := admin.create()
	g := &group{h: h, m: m}
	g.observer = admin.connect(m)
	g.connections = append(g.connections, g.observer)
	for _, name := range names {
		g.add(h.actor(name))
	}
	return g
}
func (g *group) add(a *actor) *connection {
	g.h.t.Helper()
	a.join(g.m)
	current := g.observer.actor.view(g.m)
	g.sync(current.Revision)
	c := a.connect(g.m)
	g.connections = append(g.connections, c)
	g.audit()
	return c
}
func (g *group) attach(a *actor) *connection {
	c := a.connect(g.m)
	g.connections = append(g.connections, c)
	g.audit()
	return c
}
func (g *group) sync(revision string) {
	g.h.t.Helper()
	for _, c := range g.connections {
		if !c.closed.Load() {
			c.sync(revision)
		}
	}
	g.audit()
}
func (g *group) peek() snapshot {
	g.h.t.Helper()
	for _, c := range g.connections {
		if !c.closed.Load() && c.state.Role == "spectator" {
			return c.state
		}
	}
	g.h.t.Fatal("fixture requires an authorized spectator oracle")
	return snapshot{}
}
func (g *group) audit() {
	g.h.t.Helper()
	oracle := g.peek()
	for _, c := range g.connections {
		if !c.closed.Load() {
			if err := compareAudience(c.state, oracle); err != nil {
				g.h.t.Fatal(err)
			}
		}
	}
}
func (g *group) byID(id string) *connection {
	for _, c := range g.connections {
		if c.actor.id == id && !c.closed.Load() {
			return c
		}
	}
	g.h.t.Fatal("no live client for game actor")
	return nil
}
func (g *group) change(c *connection, action string, data any) (command, message) {
	g.h.t.Helper()
	cmd := c.command(action, data)
	ack := c.apply(cmd)
	requireAck(g.h.t, ack)
	if ack.Replayed || ack.Changed == nil || !*ack.Changed {
		g.h.t.Fatal("new mutation was not applied once")
	}
	g.sync(ack.Revision)
	return cmd, ack
}
func requireAck(t *testing.T, m message) {
	t.Helper()
	if m.Type != "ack" || m.Error != nil {
		t.Fatalf("expected ack, got %+v", m.Error)
	}
}
func requireError(t *testing.T, m message, code string) {
	t.Helper()
	if m.Type != "error" || m.Error == nil || m.Error.Code != code {
		t.Fatalf("expected %s, got %+v", code, m.Error)
	}
}
func (g *group) start(players []*connection, starter *connection) (command, message) {
	g.h.t.Helper()
	seats := make([]string, len(players))
	for i, c := range players {
		g.change(c, "ready", map[string]bool{"ready": true})
		seats[i] = c.actor.id
	}
	cmd, ack := g.change(g.observer, "start", map[string]any{"seat_order": seats, "shuffle_seats": false, "first_bidder": starter.actor.id})
	if g.observer.state.Game.RoundID != "1" || *g.observer.state.Game.CurrentPlayerID != starter.actor.id || g.observer.state.Game.EliminationLimit != min(6, 24/len(players)+1) {
		g.h.t.Fatal("start did not deal/open round one")
	}
	for i, p := range g.observer.state.Game.Players {
		if p.PlayerID != seats[i] || p.HandCount != 1 || p.RequiredHandSize != 1 {
			g.h.t.Fatal("start ignored configured seats or initial hand sizes")
		}
	}
	return cmd, ack
}

func (g *group) startRandom(players []*connection) {
	g.h.t.Helper()
	for _, c := range players {
		g.change(c, "ready", map[string]bool{"ready": true})
	}
	g.change(g.observer, "start", map[string]any{"seat_order": nil, "shuffle_seats": true, "first_bidder": nil})
	want := make(map[string]bool)
	for _, c := range players {
		want[c.actor.id] = true
	}
	for _, p := range g.observer.state.Game.Players {
		if !want[p.PlayerID] || p.HandCount != 1 {
			g.h.t.Fatal("random start used incorrect participants/deal")
		}
		delete(want, p.PlayerID)
	}
	if len(want) != 0 {
		g.h.t.Fatal("random seating omitted a ready player")
	}
}
