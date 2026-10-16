package transport

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkucharz/falsepoker/internal/game"
	"github.com/gorilla/websocket"
)

type wireMessage struct {
	State roomState `json:"state"`
	socketOutcome
}

type testSocket struct {
	conn  *websocket.Conn
	state roomState
}

func (f *fixture) dial(client *http.Client, membership membershipResponse, headers http.Header, suffix string) (*websocket.Conn, *http.Response, error) {
	u, _ := url.Parse(f.server.URL)
	if headers == nil {
		headers = make(http.Header)
	}
	if _, ok := headers["Origin"]; !ok {
		headers.Set("Origin", f.app.origin)
	}
	for _, cookie := range client.Jar.Cookies(u) {
		headers.Add("Cookie", cookie.String())
	}
	dialer := websocket.Dialer{Subprotocols: []string{socketProtocol}, HandshakeTimeout: 3 * time.Second}
	if values, exists := headers["Sec-Websocket-Protocol"]; exists {
		dialer.Subprotocols = nil
		for _, value := range values {
			if value != "" {
				dialer.Subprotocols = append(dialer.Subprotocols, value)
			}
		}
		delete(headers, "Sec-Websocket-Protocol")
	}
	if transport, ok := client.Transport.(*http.Transport); ok {
		dialer.TLSClientConfig = transport.TLSClientConfig
	}
	address := strings.Replace(f.server.URL, "http", "ws", 1) + "/api/v1/rooms/" + membership.RoomCode + "/ws?room_id=" + url.QueryEscape(membership.RoomID) + suffix
	return dialer.Dial(address, headers)
}

func (f *fixture) connect(t *testing.T, client *http.Client, membership membershipResponse) *testSocket {
	t.Helper()
	conn, response, err := f.dial(client, membership, nil, "")
	if err != nil {
		if response != nil {
			data, _ := io.ReadAll(response.Body)
			response.Body.Close()
			t.Fatalf("upgrade: %v: %s", err, data)
		}
		t.Fatal(err)
	}
	if conn.Subprotocol() != socketProtocol {
		t.Fatal("missing negotiated protocol")
	}
	t.Cleanup(func() { conn.Close() })
	s := &testSocket{conn: conn}
	first := s.read(t)
	if first.Type != "snapshot" || first.State.RecipientID == "" {
		t.Fatalf("initial message: %+v", first)
	}
	return s
}

func (s *testSocket) read(t *testing.T) wireMessage {
	t.Helper()
	if err := s.conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, data, err := s.conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > maxOutputBytes || strings.Contains(string(data), "Undealt") || strings.Contains(string(data), CookieName) {
		t.Fatalf("unsafe output: %s", data)
	}
	var m wireMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m.Type == "snapshot" {
		before, _ := strconvCounter(s.state.Revision)
		after, _ := strconvCounter(m.State.Revision)
		if s.state.Revision != "" && after <= before {
			t.Fatalf("unordered snapshots: %s -> %s", s.state.Revision, m.State.Revision)
		}
		s.state = m.State
	}
	return m
}

func strconvCounter(s string) (uint64, bool) { raw, _ := json.Marshal(s); return decimalValue(raw) }

func (s *testSocket) sync(t *testing.T, revision string) {
	t.Helper()
	target, _ := strconvCounter(revision)
	for {
		current, _ := strconvCounter(s.state.Revision)
		if current >= target {
			return
		}
		s.read(t)
	}
}

func commandBody(id string, state roomState, action string, data any) []byte {
	round := "0"
	if state.Game != nil {
		round = state.Game.RoundID
	}
	body, _ := json.Marshal(map[string]any{"type": "command", "request_id": id, "room_id": state.RoomID, "expected_revision": state.Revision, "game_id": state.GameID, "round_id": round, "action": action, "data": data})
	return body
}

func (s *testSocket) send(t *testing.T, body []byte) socketOutcome {
	t.Helper()
	if err := s.conn.WriteMessage(websocket.TextMessage, body); err != nil {
		t.Fatal(err)
	}
	return s.outcome(t)
}

func (s *testSocket) outcome(t *testing.T) socketOutcome {
	t.Helper()
	for {
		m := s.read(t)
		if m.Type == "ack" || m.Type == "error" {
			if m.Type == "ack" && m.Changed != nil && *m.Changed && !m.Replayed {
				s.sync(t, m.Revision)
			}
			return m.socketOutcome
		}
	}
}

func (f *fixture) command(t *testing.T, s *testSocket, action string, data any) socketOutcome {
	t.Helper()
	return s.send(t, commandBody(f.key(), s.state, action, data))
}

func requireAck(t *testing.T, result socketOutcome) {
	t.Helper()
	if result.Type != "ack" || result.Error != nil {
		t.Fatalf("expected ack: %+v", result)
	}
}
func requireSocketError(t *testing.T, result socketOutcome, code string) {
	t.Helper()
	if result.Type != "error" || result.Error == nil || result.Error.Code != code {
		t.Fatalf("expected %s: %+v", code, result)
	}
}

func (f *fixture) joinSocketRoom(t *testing.T, client *http.Client, membership membershipResponse, name string) {
	t.Helper()
	result := f.request(t, client, "POST", "/api/v1/rooms/"+membership.RoomCode+"/join", fmt.Sprintf(`{"display_name":%q}`, name), f.key(), nil)
	if result.status != 200 && result.status != 201 {
		t.Fatalf("join: %+v", result)
	}
}

func TestWebSocketGameplayPrivacyAndReplay(t *testing.T) {
	f := newFixture(t, Config{}, false)
	alice, aliceID := f.actor(t)
	bob, bobID := f.actor(t)
	watcher, watcherID := f.actor(t)
	membership := f.create(t, alice, "Alice")
	f.joinSocketRoom(t, bob, membership, "Bob")
	f.joinSocketRoom(t, watcher, membership, "Watcher")
	a := f.connect(t, alice, membership)
	b := f.connect(t, bob, membership)
	w := f.connect(t, watcher, membership)
	requireAck(t, f.command(t, a, "ready", map[string]any{"ready": true}))
	b.sync(t, a.state.Revision)
	w.sync(t, a.state.Revision)
	requireAck(t, f.command(t, b, "ready", map[string]any{"ready": true}))
	a.sync(t, b.state.Revision)
	w.sync(t, b.state.Revision)
	requireSocketError(t, f.command(t, b, "start", map[string]any{"seat_order": []string{aliceID, bobID}, "shuffle_seats": false, "first_bidder": aliceID}), "NOT_ADMIN")
	requireAck(t, f.command(t, a, "start", map[string]any{"seat_order": []string{aliceID, bobID}, "shuffle_seats": false, "first_bidder": aliceID}))
	b.sync(t, a.state.Revision)
	w.sync(t, a.state.Revision)
	if a.state.Role != "participant" || b.state.Role != "participant" || w.state.Role != "spectator" || w.state.RecipientID != watcherID {
		t.Fatal("wrong roles")
	}
	for i, player := range a.state.Game.Players {
		if (player.PlayerID == aliceID) != (player.Hand != nil) {
			t.Fatal("Alice saw wrong hand")
		}
		if (b.state.Game.Players[i].PlayerID == bobID) != (b.state.Game.Players[i].Hand != nil) {
			t.Fatal("Bob saw wrong hand")
		}
		if len(w.state.Game.Players[i].Hand) != 1 {
			t.Fatal("spectator cannot peek")
		}
	}
	outOfTurn := commandBody(f.key(), b.state, "bid", map[string]any{"bid": bidState{Category: "high_card", PrimaryRank: "9"}})
	requireSocketError(t, b.send(t, outOfTurn), "OUT_OF_TURN")
	requireSocketError(t, f.command(t, w, "challenge", map[string]any{}), "NOT_PARTICIPANT")
	// A five-card claim is certainly false with only two dealt cards.
	body := commandBody(f.key(), a.state, "bid", map[string]any{"bid": bidState{Category: "royal_straight_flush", Suit: "clubs"}})
	ack := a.send(t, body)
	requireAck(t, ack)
	b.sync(t, ack.Revision)
	w.sync(t, ack.Revision)
	previousRejection := b.send(t, outOfTurn)
	requireSocketError(t, previousRejection, "OUT_OF_TURN")
	if !previousRejection.Replayed {
		t.Fatal("old rejection was re-evaluated after turn changed")
	}
	requireAck(t, f.command(t, b, "challenge", map[string]any{}))
	a.sync(t, b.state.Revision)
	w.sync(t, b.state.Revision)
	if a.state.Game.RoundPhase != "resolution" || a.state.Game.LastResult.LoserID != aliceID {
		t.Fatal("challenge did not resolve")
	}
	for _, player := range a.state.Game.Players {
		if player.Hand == nil {
			t.Fatal("resolution did not reveal hands")
		}
	}
	requireSocketError(t, f.command(t, b, "next_round", map[string]any{}), "OUT_OF_TURN")
	requireAck(t, f.command(t, a, "next_round", map[string]any{}))
	b.sync(t, a.state.Revision)
	w.sync(t, a.state.Revision)
	if a.state.Game.RoundID != "2" || a.state.Game.Players[0].HandCount != 2 || a.state.Game.Players[1].Hand != nil || a.state.Game.LastResult.RoundID != "1" {
		t.Fatal("next round/privacy/history incorrect")
	}
	// Another tab/reconnect shares identity, initial current state, and retry cache.
	a2 := f.connect(t, alice, membership)
	if !reflect.DeepEqual(a.state, a2.state) {
		t.Fatal("second tab changed state or hand")
	}
	replayed := a2.send(t, body)
	requireAck(t, replayed)
	if !replayed.Replayed || replayed.Revision != ack.Revision || replayed.RequestID == nil {
		t.Fatalf("lost-ack retry: %+v", replayed)
	}
	if a2.state.Game.RoundID != "2" {
		t.Fatal("retry rolled back state")
	}
	if err := a2.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1000, "reconnect")); err != nil {
		t.Fatal(err)
	}
	requireClose(t, a2, 1000)
	a2 = f.connect(t, alice, membership)
	if got := a2.send(t, body); !got.Replayed || got.Revision != ack.Revision {
		t.Fatalf("reconnect retry: %+v", got)
	}
	// Normalized JSON retry retains the same outcome despite whitespace/key order.
	var normalized map[string]any
	json.Unmarshal(body, &normalized)
	pretty, _ := json.MarshalIndent(normalized, "", "  ")
	if got := a2.send(t, pretty); !got.Replayed || got.Revision != ack.Revision {
		t.Fatalf("normalized retry: %+v", got)
	}
	// Reusing that ID for a new intent is an error, without replacing the record.
	var original map[string]any
	json.Unmarshal(body, &original)
	original["action"] = "challenge"
	original["data"] = map[string]any{}
	conflict, _ := json.Marshal(original)
	requireSocketError(t, a2.send(t, conflict), "REQUEST_ID_REUSED")
	requireSocketError(t, a2.send(t, commandBody(f.key(), roomState{RoomID: a.state.RoomID, Revision: a.state.Revision, GameID: "0"}, "ready", map[string]any{"ready": true})), "STALE_GAME")
	oldRound := a.state
	copyGame := *a.state.Game
	copyGame.RoundID = "1"
	oldRound.Game = &copyGame
	requireSocketError(t, a2.send(t, commandBody(f.key(), oldRound, "challenge", map[string]any{})), "STALE_ROUND")
	// A spectator joining changes the whole-room revision and invalidates intent.
	stale := commandBody(f.key(), a.state, "challenge", map[string]any{})
	other, _ := f.actor(t)
	f.joinSocketRoom(t, other, membership, "New watcher")
	requireSocketError(t, a2.send(t, stale), "STALE_STATE")
	// Explicit departure still uses the room's withdrawal/win path and closes
	// every tab for that membership, without disturbing spectators.
	left := f.request(t, alice, "POST", "/api/v1/rooms/"+membership.RoomCode+"/leave", fmt.Sprintf(`{"room_id":%q}`, membership.RoomID), f.key(), nil)
	if left.status != 200 {
		t.Fatal(left.body)
	}
	requireClose(t, a, 4003)
	requireClose(t, a2, 4003)
	final := f.state(t, bob, membership.RoomCode)
	b.sync(t, final.Revision)
	w.sync(t, final.Revision)
	if b.state.Game.Phase != "finished" || *b.state.Game.WinnerID != bobID {
		t.Fatal("departure did not finish game")
	}
	for _, member := range b.state.Members {
		if member.Ready {
			t.Fatal("finished game retained readiness")
		}
	}
}

func TestWebSocketConcurrentDuplicateAndStaleCommands(t *testing.T) {
	f := newFixture(t, Config{}, false)
	client, _ := f.actor(t)
	other, _ := f.actor(t)
	m := f.create(t, client, "Owner")
	f.joinSocketRoom(t, other, m, "Other")
	a := f.connect(t, client, m)
	a2 := f.connect(t, client, m)
	b := f.connect(t, other, m)
	body := commandBody(f.key(), a.state, "ready", map[string]any{"ready": true})
	if err := workers(2, func(i int) error { return []*testSocket{a, a2}[i].conn.WriteMessage(websocket.TextMessage, body) }); err != nil {
		t.Fatal(err)
	}
	one, two := a.outcome(t), a2.outcome(t)
	requireAck(t, one)
	requireAck(t, two)
	if one.Replayed == two.Replayed || one.Revision != two.Revision {
		t.Fatalf("duplicate outcomes: %+v %+v", one, two)
	}
	b.sync(t, one.Revision)
	// Two different members concurrently act on the same observed revision.
	commands := [][]byte{commandBody(f.key(), a.state, "ready", map[string]any{"ready": false}), commandBody(f.key(), b.state, "ready", map[string]any{"ready": true})}
	if err := workers(2, func(i int) error { return []*testSocket{a, b}[i].conn.WriteMessage(websocket.TextMessage, commands[i]) }); err != nil {
		t.Fatal(err)
	}
	x, y := a.outcome(t), b.outcome(t)
	if x.Type == "ack" {
		requireSocketError(t, y, "STALE_STATE")
	} else {
		requireSocketError(t, x, "STALE_STATE")
		requireAck(t, y)
	}
	final := f.state(t, client, m.RoomCode)
	revision, _ := strconvCounter(one.Revision)
	want, _ := strconvCounter(final.Revision)
	if want != revision+1 {
		t.Fatal("concurrent stale commands both mutated")
	}
}

func TestWebSocketHeartbeat(t *testing.T) {
	t.Run("matching pong keeps connection alive", func(t *testing.T) {
		f := newFixture(t, Config{}, false)
		f.app.timing = socketTiming{30 * time.Millisecond, 300 * time.Millisecond, time.Second}
		client, _ := f.actor(t)
		m := f.create(t, client, "Owner")
		s := f.connect(t, client, m)
		var pings atomic.Int32
		pong := make(chan string, 1)
		s.conn.SetPongHandler(func(data string) error { pong <- data; return nil })
		s.conn.SetPingHandler(func(data string) error {
			pings.Add(1)
			return s.conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
		})
		read := make(chan error, 1)
		go func() { _, _, err := s.conn.ReadMessage(); read <- err }()
		timer := time.NewTimer(650 * time.Millisecond)
		defer timer.Stop()
		select {
		case err := <-read:
			t.Fatalf("healthy connection closed: %v", err)
		case <-timer.C:
		}
		if pings.Load() < 3 {
			t.Fatal("heartbeats not sent")
		}
		// Client Ping is replied to by the same serialized server writer.
		if err := s.conn.WriteControl(websocket.PingMessage, []byte("client-ping"), time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		select {
		case data := <-pong:
			if data != "client-ping" {
				t.Fatal(data)
			}
		case <-time.After(time.Second):
			t.Fatal("client ping was not answered")
		}
		s.conn.Close()
		select {
		case <-read:
		case <-time.After(time.Second):
			t.Fatal("reader did not stop")
		}
	})
	t.Run("unmatched pong cannot renew deadline", func(t *testing.T) {
		f := newFixture(t, Config{}, false)
		f.app.timing = socketTiming{30 * time.Millisecond, 200 * time.Millisecond, time.Second}
		client, _ := f.actor(t)
		m := f.create(t, client, "Owner")
		s := f.connect(t, client, m)
		s.conn.SetPingHandler(func(_ string) error {
			return s.conn.WriteControl(websocket.PongMessage, []byte("wrong-nonce"), time.Now().Add(time.Second))
		})
		requireClose(t, s, 1000)
	})
}

func TestSocketOutputOverflowClosesOnlyThatSubscription(t *testing.T) {
	f := newFixture(t, Config{}, false)
	client, _ := f.actor(t)
	m := f.create(t, client, "Owner")
	current, err := f.app.registry.Lookup(m.RoomCode)
	if err != nil {
		t.Fatal(err)
	}
	state := f.state(t, client, m.RoomCode)
	sub, err := current.Subscribe(game.PlayerID(state.RecipientID))
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	// No writer drains this bounded transport queue. Testing it directly avoids
	// depending on platform-specific TCP buffer sizes to simulate a stalled peer.
	c := &socket{sub: sub, out: make(chan outputFrame, outputCapacity), done: make(chan struct{})}
	for i := 0; i <= outputCapacity; i++ {
		c.enqueue(outputFrame{websocket.TextMessage, []byte("{}")})
	}
	select {
	case <-c.done:
	default:
		t.Fatal("output overflow did not close socket")
	}
	if c.closeCode != 4008 {
		t.Fatalf("close code=%d", c.closeCode)
	}
	if got := f.state(t, client, m.RoomCode); !reflect.DeepEqual(state, got) {
		t.Fatal("slow socket changed membership or revision")
	}
	healthy := f.connect(t, client, m)
	requireAck(t, f.command(t, healthy, "ready", map[string]any{"ready": true}))
}

func TestWebSocketHandshakeGuards(t *testing.T) {
	f := newFixture(t, Config{}, true)
	owner, _ := f.actor(t)
	outsider, _ := f.actor(t)
	membership := f.create(t, owner, "Owner")
	tests := []struct {
		name       string
		client     *http.Client
		membership membershipResponse
		headers    http.Header
		suffix     string
		status     int
		code       string
	}{
		{"origin missing", owner, membership, http.Header{"Origin": []string{""}}, "", 403, "ORIGIN_NOT_ALLOWED"},
		{"origin foreign", owner, membership, http.Header{"Origin": []string{"https://evil.example"}}, "", 403, "ORIGIN_NOT_ALLOWED"},
		{"origin multiple", owner, membership, http.Header{"Origin": []string{f.app.origin, f.app.origin}}, "", 403, "ORIGIN_NOT_ALLOWED"},
		{"unauthenticated", f.client(t), membership, nil, "", 401, "AUTH_REQUIRED"},
		{"nonmember", outsider, membership, nil, "", 404, "ROOM_UNAVAILABLE"},
		{"stale lifetime", owner, membershipResponse{RoomCode: membership.RoomCode, RoomID: "old"}, nil, "", 409, "STALE_ROOM"},
		{"unknown room", owner, membershipResponse{RoomCode: "000000000000", RoomID: "old"}, nil, "", 404, "ROOM_UNAVAILABLE"},
		{"extra query", owner, membership, nil, "&player_id=someone", 400, "INVALID_REQUEST"},
		{"duplicate query", owner, membership, nil, "&room_id=other", 400, "INVALID_REQUEST"},
		{"missing protocol", owner, membership, http.Header{"Sec-Websocket-Protocol": []string{""}}, "", 400, "UNSUPPORTED_PROTOCOL"},
		{"unsupported protocol", owner, membership, http.Header{"Sec-Websocket-Protocol": []string{"falsepoker.v2"}}, "", 400, "UNSUPPORTED_PROTOCOL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, response, err := f.dial(tt.client, tt.membership, tt.headers, tt.suffix)
			if conn != nil {
				conn.Close()
				t.Fatal("invalid upgrade accepted")
			}
			if err == nil || response == nil {
				t.Fatalf("upgrade: %v", err)
			}
			defer response.Body.Close()
			body, readErr := io.ReadAll(response.Body)
			if readErr != nil {
				t.Fatal(readErr)
			}
			errorCode(t, received{status: response.StatusCode, body: string(body)}, tt.status, tt.code)
		})
	}
	s := f.connect(t, owner, membership)
	if s.state.RecipientID == "" {
		t.Fatal("secure upgrade lost identity")
	}
}

func TestWebSocketRoomIsolationAndDeparture(t *testing.T) {
	f := newFixture(t, Config{}, false)
	client, _ := f.actor(t)
	other, _ := f.actor(t)
	one := f.create(t, client, "One")
	two := f.create(t, other, "Two")
	a := f.connect(t, client, one)
	b := f.connect(t, other, two)
	wrong := a.state
	wrong.RoomID = two.RoomID
	requireSocketError(t, a.send(t, commandBody(f.key(), wrong, "ready", map[string]any{"ready": true})), "STALE_ROOM")
	requireAck(t, f.command(t, a, "ready", map[string]any{"ready": true}))
	if got := f.state(t, other, two.RoomCode); !reflect.DeepEqual(got, b.state) {
		t.Fatal("command crossed rooms")
	}
	a2 := f.connect(t, client, one)
	left := f.request(t, client, "POST", "/api/v1/rooms/"+one.RoomCode+"/leave", fmt.Sprintf(`{"room_id":%q}`, one.RoomID), f.key(), nil)
	if left.status != 200 {
		t.Fatal(left.body)
	}
	for _, conn := range []*testSocket{a, a2} {
		requireClose(t, conn, 4004)
	}
	requireAck(t, f.command(t, b, "ready", map[string]any{"ready": true}))
}

func requireClose(t *testing.T, s *testSocket, code int) {
	t.Helper()
	s.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, _, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, code) {
				t.Fatalf("close: %v, want %d", err, code)
			}
			return
		}
	}
}

func TestWebSocketInvalidMessages(t *testing.T) {
	tests := []struct {
		name string
		kind int
		body []byte
		code int
	}{
		{"binary", websocket.BinaryMessage, []byte("{}"), 1003},
		{"UTF8", websocket.TextMessage, []byte{0xff}, 1007},
		{"oversized", websocket.TextMessage, []byte(strings.Repeat(" ", maxBodyBytes+1)), 1009},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, Config{}, false)
			client, _ := f.actor(t)
			m := f.create(t, client, "Owner")
			s := f.connect(t, client, m)
			if err := s.conn.WriteMessage(tt.kind, tt.body); err != nil {
				t.Fatal(err)
			}
			requireClose(t, s, tt.code)
		})
	}
	t.Run("three malformed", func(t *testing.T) {
		f := newFixture(t, Config{}, false)
		client, _ := f.actor(t)
		m := f.create(t, client, "Owner")
		s := f.connect(t, client, m)
		for i := 0; i < 2; i++ {
			requireSocketError(t, s.send(t, []byte(`{"type":"command","player_id":"victim"}`)), "INVALID_REQUEST")
		}
		requireAck(t, f.command(t, s, "ready", map[string]any{"ready": false}))
		for i := 0; i < 2; i++ {
			requireSocketError(t, s.send(t, []byte(`[]`)), "INVALID_REQUEST")
		}
		if err := s.conn.WriteMessage(websocket.TextMessage, []byte(`[]`)); err != nil {
			t.Fatal(err)
		}
		requireClose(t, s, 1008)
		if got := f.state(t, client, m.RoomCode); got.Revision != "1" {
			t.Fatal("malformed input mutated room")
		}
	})
}

func TestWebSocketLimitsRateAndShutdown(t *testing.T) {
	t.Run("connections", func(t *testing.T) {
		f := newFixture(t, Config{Limits: Limits{MaxConnections: 3, MaxConnectionsPerMember: 2}}, false)
		client, _ := f.actor(t)
		m := f.create(t, client, "Owner")
		a := f.connect(t, client, m)
		f.connect(t, client, m)
		conn, response, err := f.dial(client, m, nil, "")
		if conn != nil {
			conn.Close()
			t.Fatal("member limit bypassed")
		}
		if err == nil || response.StatusCode != 429 {
			t.Fatalf("limit: %v", err)
		}
		response.Body.Close()
		other, _ := f.actor(t)
		n := f.create(t, other, "Other")
		f.connect(t, other, n)
		conn, response, err = f.dial(other, n, nil, "")
		if conn != nil {
			conn.Close()
			t.Fatal("process limit bypassed")
		}
		if err == nil || response.StatusCode != 429 {
			t.Fatalf("limit: %v", err)
		}
		response.Body.Close()
		// A completed normal close releases capacity without changing membership.
		if err := a.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1000, "bye")); err != nil {
			t.Fatal(err)
		}
		requireClose(t, a, 1000)
		f.connect(t, client, m)
	})
	t.Run("shared rate", func(t *testing.T) {
		clock := newClock()
		f := newFixture(t, Config{Now: clock.now, Limits: Limits{MutationRate: 1, MutationBurst: 2}}, false)
		client, _ := f.actor(t)
		m := f.create(t, client, "Owner")
		s := f.connect(t, client, m)
		body := commandBody(f.key(), s.state, "ready", map[string]any{"ready": false})
		requireAck(t, s.send(t, body)) // create and this no-op consume the shared burst
		for i := 0; i < 2; i++ {
			got := s.send(t, body)
			requireSocketError(t, got, "RATE_LIMITED")
			if !got.Error.Retryable || got.RetryAfterMS <= 0 {
				t.Fatal("missing admission retry hint")
			}
		}
		if err := s.conn.WriteMessage(websocket.TextMessage, body); err != nil {
			t.Fatal(err)
		}
		requireClose(t, s, 1008)
		clock.advance(time.Second)
		reconnected := f.connect(t, client, m)
		if got := reconnected.send(t, body); !got.Replayed {
			t.Fatal("rate failures replaced cached outcome")
		}
	})
	t.Run("shutdown", func(t *testing.T) {
		f := newFixture(t, Config{}, false)
		client, _ := f.actor(t)
		m := f.create(t, client, "Owner")
		s := f.connect(t, client, m)
		closed := make(chan struct{})
		go func() { f.app.Close(); close(closed) }()
		requireClose(t, s, 1001)
		select {
		case <-closed:
		case <-time.After(3 * time.Second):
			t.Fatal("shutdown leaked connection goroutines")
		}
		result := f.request(t, client, "GET", "/health", "", "", nil)
		errorCode(t, result, 503, "SHUTTING_DOWN")
	})
}
