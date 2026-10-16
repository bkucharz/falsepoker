package transport

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bkucharz/falsepoker/internal/room"
	"github.com/gorilla/websocket"
)

func closeTab(t *testing.T, s *testSocket) {
	t.Helper()
	if err := s.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1000, "close tab")); err != nil {
		t.Fatal(err)
	}
	requireClose(t, s, 1000)
}

func dialError(t *testing.T, f *fixture, client *http.Client, m membershipResponse, status int, code string) {
	t.Helper()
	conn, response, err := f.dial(client, m, nil, "")
	if conn != nil {
		conn.Close()
		t.Fatal("unavailable membership upgraded")
	}
	if err == nil || response == nil {
		t.Fatalf("upgrade failure: %v", err)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		t.Fatal(readErr)
	}
	result := received{status: response.StatusCode, body: string(body)}
	errorCode(t, result, status, code)
}

func TestReconnectRestoresPrivateHandAndRoundHistory(t *testing.T) {
	clock := newClock()
	f := newFixture(t, Config{Clock: clock}, false)
	alice, aliceID := f.actor(t)
	bob, bobID := f.actor(t)
	m := f.create(t, alice, "Alice")
	f.joinSocketRoom(t, bob, m, "Bob")
	a := f.connect(t, alice, m)
	b := f.connect(t, bob, m)
	requireAck(t, f.command(t, a, "ready", map[string]any{"ready": true}))
	b.sync(t, a.state.Revision)
	requireAck(t, f.command(t, b, "ready", map[string]any{"ready": true}))
	a.sync(t, b.state.Revision)
	requireAck(t, f.command(t, a, "start", map[string]any{"seat_order": []string{aliceID, bobID}, "shuffle_seats": false, "first_bidder": aliceID}))
	b.sync(t, a.state.Revision)
	requireAck(t, f.command(t, a, "bid", map[string]any{"bid": bidState{Category: "royal_straight_flush", Suit: "clubs"}}))
	b.sync(t, a.state.Revision)
	requireAck(t, f.command(t, b, "challenge", map[string]any{}))
	a.sync(t, b.state.Revision)
	requireAck(t, f.command(t, a, "next_round", map[string]any{}))
	b.sync(t, a.state.Revision)
	before := a.state
	secondTab := f.connect(t, alice, m)
	closeTab(t, a)
	clock.Advance(3 * room.ReconnectGrace)
	if clock.Pending() != 0 || !reflect.DeepEqual(f.state(t, alice, m.RoomCode), before) {
		t.Fatal("another live tab did not preserve membership")
	}
	closeTab(t, secondTab)
	clock.Advance(room.ReconnectGrace - time.Nanosecond)
	if !reflect.DeepEqual(f.state(t, bob, m.RoomCode), b.state) {
		t.Fatal("disconnect advanced game or skipped turn during grace")
	}
	reconnected := f.connect(t, alice, m)
	if !reflect.DeepEqual(reconnected.state, before) || reconnected.state.Game.LastResult == nil || reconnected.state.Game.RoundID != "2" {
		t.Fatal("reconnect changed hand/seat or lost the previous result")
	}
	clock.Advance(2 * room.ReconnectGrace)
	if clock.Pending() != 0 {
		t.Fatal("successful reconnect retained an expiry timer")
	}
	// The original player can act in their unchanged turn without HTTP rejoin.
	requireAck(t, f.command(t, reconnected, "bid", map[string]any{"bid": bidState{Category: "high_card", PrimaryRank: "9"}}))
}

func TestInitialAttachmentCannotBeExtendedByHTTP(t *testing.T) {
	clock := newClock()
	f := newFixture(t, Config{Clock: clock}, false)
	owner, ownerID := f.actor(t)
	other, otherID := f.actor(t)
	m := f.create(t, owner, "Owner")
	f.joinSocketRoom(t, other, m, "Other")
	live := f.connect(t, other, m)
	clock.Advance(59 * time.Second)
	if f.state(t, owner, m.RoomCode).AdminID != ownerID {
		t.Fatal("admin transferred before expiry")
	}
	f.joinSocketRoom(t, owner, m, "Renamed")
	// An invalid upgrade must not attach or restart the deadline.
	invalid := f.request(t, owner, "GET", "/api/v1/rooms/"+m.RoomCode+"/ws?room_id="+m.RoomID, "", "", map[string]string{"Origin": f.app.origin, "Sec-WebSocket-Protocol": socketProtocol})
	errorCode(t, invalid, 400, "INVALID_REQUEST")
	clock.Elapse(time.Second) // delayed timer; admission must still enforce expiry
	dialError(t, f, owner, m, 404, "ROOM_UNAVAILABLE")
	live.sync(t, "3")
	if live.state.AdminID != otherID || len(live.state.Members) != 1 {
		t.Fatal("expiry did not transfer admin/remove member once")
	}
	errorCode(t, f.request(t, owner, "GET", "/api/v1/rooms/"+m.RoomCode, "", "", nil), 404, "ROOM_UNAVAILABLE")
	f.joinSocketRoom(t, owner, m, "Returned")
	returned := f.connect(t, owner, m)
	if returned.state.AdminID != otherID || len(returned.state.Members) != 2 || returned.state.Members[1].Ready || returned.state.Members[1].DisplayName != "Returned" {
		t.Fatal("rejoin restored old admin/readiness instead of a new membership")
	}
}

func TestExpiredRoomReleasesCapacity(t *testing.T) {
	clock := newClock()
	registry, err := room.NewRegistryWithLimits(1, 24)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, Config{Clock: clock, Registry: registry, Limits: Limits{MaxMemberships: 1}}, false)
	client, _ := f.actor(t)
	m := f.create(t, client, "Abandoned")
	clock.Advance(room.ReconnectGrace)
	if clock.Pending() != 0 {
		t.Fatal("empty room retained timer")
	}
	dialError(t, f, client, m, 404, "ROOM_UNAVAILABLE")
	errorCode(t, f.request(t, client, "GET", "/api/v1/rooms/"+m.RoomCode, "", "", nil), 404, "ROOM_UNAVAILABLE")
	fresh := f.create(t, client, "Fresh")
	if fresh.RoomID == m.RoomID {
		t.Fatal("new room reused room lifetime")
	}
	f.app.Close()
	if clock.Pending() != 0 {
		t.Fatal("shutdown did not release abandoned membership timers")
	}
}

func TestExpiryContinuesGameThenRejoinIsSpectator(t *testing.T) {
	clock := newClock()
	f := newFixture(t, Config{Clock: clock}, false)
	clients := make([]*http.Client, 3)
	ids := make([]string, 3)
	for i := range clients {
		clients[i], ids[i] = f.actor(t)
	}
	m := f.create(t, clients[0], "A")
	f.joinSocketRoom(t, clients[1], m, "B")
	f.joinSocketRoom(t, clients[2], m, "C")
	sockets := make([]*testSocket, 3)
	for i := range sockets {
		sockets[i] = f.connect(t, clients[i], m)
	}
	for i := range sockets {
		if i > 0 {
			sockets[i].sync(t, sockets[i-1].state.Revision)
		}
		requireAck(t, f.command(t, sockets[i], "ready", map[string]any{"ready": true}))
	}
	sockets[0].sync(t, sockets[2].state.Revision)
	requireAck(t, f.command(t, sockets[0], "start", map[string]any{"seat_order": ids, "shuffle_seats": false, "first_bidder": ids[0]}))
	for i := 1; i < 3; i++ {
		sockets[i].sync(t, sockets[0].state.Revision)
	}
	requireAck(t, f.command(t, sockets[0], "bid", map[string]any{"bid": bidState{Category: "high_card", PrimaryRank: "9"}}))
	closeTab(t, sockets[0])
	sockets[1].sync(t, sockets[0].state.Revision)
	requireAck(t, f.command(t, sockets[1], "bid", map[string]any{"bid": bidState{Category: "pair", PrimaryRank: "9"}}))
	sockets[2].sync(t, sockets[1].state.Revision)
	requireAck(t, f.command(t, sockets[2], "bid", map[string]any{"bid": bidState{Category: "royal_straight_flush", Suit: "clubs"}}))
	sockets[1].sync(t, sockets[2].state.Revision)
	if *sockets[1].state.Game.CurrentPlayerID != ids[0] {
		t.Fatal("disconnected participant's turn was skipped during grace")
	}
	clock.Advance(room.ReconnectGrace)
	current := f.state(t, clients[1], m.RoomCode)
	sockets[1].sync(t, current.Revision)
	sockets[2].sync(t, current.Revision)
	if *current.Game.CurrentPlayerID != ids[1] || !current.Game.Players[0].Withdrawn || current.Game.Players[0].HandCount != 1 {
		t.Fatal("expiry lost cards or failed to skip departed turn")
	}
	dialError(t, f, clients[0], m, 404, "ROOM_UNAVAILABLE")
	f.joinSocketRoom(t, clients[0], m, "Returned A")
	returned := f.connect(t, clients[0], m)
	if returned.state.Role != "spectator" || !returned.state.Game.Players[0].Withdrawn {
		t.Fatal("expired participant restored their former seat")
	}
	for _, player := range returned.state.Game.Players[1:] {
		if player.Hand == nil {
			t.Fatal("returning spectator did not receive authorized peek hands")
		}
	}
	sockets[1].sync(t, returned.state.Revision)
	requireSocketError(t, f.command(t, returned, "challenge", map[string]any{}), "INACTIVE_PLAYER")
	requireAck(t, f.command(t, sockets[1], "challenge", map[string]any{}))
	if len(sockets[1].state.Game.LastResult.RevealedHands) != 3 || sockets[1].state.Game.LastResult.LoserID != ids[2] {
		t.Fatal("withdrawn cards disappeared from challenge")
	}
}

func TestShutdownAndRestartLoseRoomsAndCredentials(t *testing.T) {
	clock := newClock()
	f := newFixture(t, Config{Clock: clock}, false)
	client, oldID := f.actor(t)
	m := f.create(t, client, "Owner")
	s := f.connect(t, client, m)
	other, _ := f.actor(t)
	f.create(t, other, "Never attached")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); f.app.Close() }()
	go func() { defer wg.Done(); f.app.Close() }()
	requireClose(t, s, 1001)
	wg.Wait()
	if clock.Pending() != 0 {
		t.Fatal("shutdown retained timers")
	}
	f.app.socketMu.Lock()
	count := f.app.socketCount
	f.app.socketMu.Unlock()
	if count != 0 {
		t.Fatal("shutdown leaked socket handlers")
	}
	f.app.mu.Lock()
	retained := len(f.app.sessions) + len(f.app.players) + len(f.app.bootstrap)
	f.app.mu.Unlock()
	if retained != 0 {
		t.Fatal("shutdown retained anonymous sessions")
	}
	if sess, _, failure := f.app.newSession("127.0.0.1:1234"); sess != nil || failure == nil || failure.code != "SHUTTING_DOWN" {
		t.Fatal("a delayed bootstrap recreated state after shutdown")
	}
	if _, err := f.app.registry.Lookup(m.RoomCode); err == nil {
		t.Fatal("shutdown retained rooms")
	}
	clock.Advance(2 * room.ReconnectGrace)
	restarted := newFixture(t, Config{}, false)
	dialError(t, restarted, client, m, 401, "SESSION_EXPIRED")
	errorCode(t, restarted.request(t, client, "GET", "/api/v1/rooms/"+m.RoomCode, "", "", nil), 401, "SESSION_EXPIRED")
	initialized := restarted.request(t, client, "POST", "/api/v1/session", "{}", "", nil)
	if initialized.status != 201 || strings.Contains(initialized.body, oldID) {
		t.Fatal("restart recovered an obsolete identity")
	}
	errorCode(t, restarted.request(t, client, "GET", "/api/v1/rooms/"+m.RoomCode, "", "", nil), 404, "ROOM_UNAVAILABLE")
}

func TestExpirySocketRaceNeverResurrectsMembership(t *testing.T) {
	clock := newClock()
	f := newFixture(t, Config{Clock: clock}, false)
	client, _ := f.actor(t)
	m := f.create(t, client, "Owner")
	s := f.connect(t, client, m)
	closeTab(t, s)
	clock.Elapse(room.ReconnectGrace)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); clock.RunDue() }()
	dialError(t, f, client, m, 404, "ROOM_UNAVAILABLE")
	wg.Wait()
	if clock.Pending() != 0 {
		t.Fatal("expired socket attachment resurrected a timer")
	}
}
