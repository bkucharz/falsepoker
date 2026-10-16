package integration_test

import (
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bkucharz/falsepoker/internal/room"
	"github.com/gorilla/websocket"
)

func TestLostChallengeAckReconnectAndRetry(t *testing.T) {
	h := newHarness(t)
	g := h.group("Alice", "Bob", "Cara")
	a, b, c := g.connections[1], g.connections[2], g.connections[3]
	g.start([]*connection{a, b, c}, a)
	g.place(g.falseBid())
	before := g.peek()
	original := b.command("challenge", struct{}{})
	b.mu.Lock()
	b.drop[original.RequestID] = true
	b.mu.Unlock()
	if err := b.send(original); err != nil {
		t.Fatal(err)
	}
	g.sync(increment(before.Revision))
	g.checkResolution(before, b.actor.id, false)
	var lost message
	select {
	case lost = <-b.dropped:
		requireAck(t, lost)
	case <-time.After(5 * time.Second):
		t.Fatal("challenge ack was not delivered to the loss filter")
	}
	if lost.Replayed || lost.Changed == nil || !*lost.Changed {
		t.Fatal("initial challenge was not applied")
	}
	b.disconnect()
	// Play continues during grace: Alice advances and bids, then Bob's turn waits.
	g.advance()
	g.place(g.trueBid())
	expected := b.actor.view(g.m)
	h.clock.Advance(room.ReconnectGrace - time.Nanosecond)
	if *g.observer.state.Game.CurrentPlayerID != b.actor.id {
		t.Fatal("disconnected turn was skipped")
	}
	reconnected := g.attach(b.actor)
	if !reflect.DeepEqual(reconnected.state, expected) || reconnected.state.RecipientID != b.actor.id || reconnected.state.Game.LastResult == nil {
		t.Fatal("reconnect lost the seat, private hand, or completed reveal")
	}
	count := reconnected.snapshots.Load()
	replayed := reconnected.apply(original)
	requireAck(t, replayed)
	if !replayed.Replayed || replayed.Revision != lost.Revision || replayed.Changed == nil || !*replayed.Changed || !reflect.DeepEqual(reconnected.actor.view(g.m), expected) || reconnected.snapshots.Load() != count {
		t.Fatal("lost challenge retry applied twice, emitted state, or replaced the original outcome")
	}
	// The original game/round/revision in a duplicate must not be rewritten.
	if original.RoundID == reconnected.state.Game.RoundID || original.ExpectedRevision == reconnected.state.Revision {
		t.Fatal("retry did not exercise an old context")
	}
	g.challenge(true)
	if findPlayer(g.observer.state.Game, b.actor.id).RequiredHandSize != 2 {
		t.Fatal("reconnected player could not take their waiting turn")
	}
}

func sendTogether(t *testing.T, connections []*connection, commands []command) {
	t.Helper()
	start := make(chan struct{})
	errors := make(chan error, len(connections))
	var wg sync.WaitGroup
	for i, c := range connections {
		wg.Add(1)
		go func(i int, c *connection) { defer wg.Done(); <-start; errors <- c.send(commands[i]) }(i, c)
	}
	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestConflictingTabsAndRoomAuthorization(t *testing.T) {
	h := newHarness(t)
	g := h.group("Alice", "Bob")
	a, b := g.connections[1], g.connections[2]
	a2 := g.attach(a.actor)
	g.start([]*connection{a, b}, a)
	other := h.group("Other Alice", "Other Bob")
	other.start([]*connection{other.connections[1], other.connections[2]}, other.connections[1])
	untouched := other.observer.actor.view(other.m)
	// Simultaneous identical commands across tabs share one committed outcome.
	before := g.observer.state.Revision
	same := a.command("bid", map[string]any{"bid": bid{Category: "high_card", PrimaryRank: "9"}})
	sendTogether(t, []*connection{a, a2}, []command{same, same})
	one, two := a.outcome(same.RequestID), a2.outcome(same.RequestID)
	requireAck(t, one)
	requireAck(t, two)
	if one.Replayed == two.Replayed || one.Revision != increment(before) || two.Revision != one.Revision {
		t.Fatal("duplicate tabs committed multiple mutations")
	}
	g.sync(one.Revision)
	// Different actions using one observed revision have one winner; the other
	// intent is rejected rather than silently applied against changed state.
	b2 := g.attach(b.actor)
	left := b.command("bid", map[string]any{"bid": bid{Category: "pair", PrimaryRank: "9"}})
	right := b2.command("bid", map[string]any{"bid": bid{Category: "pair", PrimaryRank: "10"}})
	sendTogether(t, []*connection{b, b2}, []command{left, right})
	x, y := b.outcome(left.RequestID), b2.outcome(right.RequestID)
	rejected, loserConn := left, b
	accepted := y
	if x.Type == "ack" {
		requireAck(t, x)
		requireError(t, y, "STALE_STATE")
		rejected, loserConn, accepted = right, b2, x
	} else {
		requireError(t, x, "STALE_STATE")
		requireAck(t, y)
	}
	if accepted.Replayed || accepted.Revision != increment(one.Revision) {
		t.Fatal("conflicting tabs advanced the room twice")
	}
	g.sync(accepted.Revision)
	baseline := g.observer.actor.view(g.m)
	oldError := loserConn.apply(rejected)
	requireError(t, oldError, "STALE_STATE")
	if !oldError.Replayed {
		t.Fatal("cached rejection was revalidated")
	}
	reused := rejected
	reused.Data = map[string]any{"bid": bid{Category: "pair", PrimaryRank: "A"}}
	requireError(t, loserConn.apply(reused), "REQUEST_ID_REUSED")
	if again := loserConn.apply(rejected); !again.Replayed || again.Error == nil || again.Error.Code != "STALE_STATE" {
		t.Fatal("key conflict overwrote original rejection")
	}
	requireError(t, g.observer.apply(g.observer.command("challenge", struct{}{})), "NOT_PARTICIPANT")
	requireError(t, b.apply(b.command("challenge", struct{}{})), "OUT_OF_TURN")
	wrongRoom := a.command("challenge", struct{}{})
	wrongRoom.RoomID = other.m.RoomID
	requireError(t, a.apply(wrongRoom), "STALE_ROOM")
	a.actor.cannotConnect(other.m, "ROOM_UNAVAILABLE")
	if !reflect.DeepEqual(g.observer.actor.view(g.m), baseline) || !reflect.DeepEqual(other.observer.actor.view(other.m), untouched) {
		t.Fatal("rejections/retries changed state or crossed rooms")
	}
	g.audit()
	other.audit()
}

func (a *actor) cannotConnect(m membership, code string) {
	a.h.t.Helper()
	u, _ := url.Parse(a.h.server.URL)
	headers := http.Header{"Origin": []string{a.h.server.URL}}
	for _, cookie := range a.http.Jar.Cookies(u) {
		headers.Add("Cookie", cookie.String())
	}
	dialer := websocket.Dialer{Subprotocols: []string{"falsepoker.v1"}, HandshakeTimeout: 5 * time.Second}
	ws, response, err := dialer.Dial(strings.Replace(a.h.server.URL, "http", "ws", 1)+"/api/v1/rooms/"+m.RoomCode+"/ws?room_id="+m.RoomID, headers)
	if ws != nil {
		ws.Close()
		a.h.t.Fatal("unauthorized/expired membership upgraded")
	}
	if err == nil || response == nil {
		a.h.t.Fatal("expected rejected upgrade")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		a.h.t.Fatal(err)
	}
	if err := a.h.inspect(data); err != nil {
		a.h.t.Fatal(err)
	}
	var failure struct {
		RequestID *string `json:"request_id"`
		Error     problem `json:"error"`
	}
	if err := decode(data, &failure); err != nil || response.StatusCode != 404 || failure.Error.Code != code {
		a.h.t.Fatalf("rejected upgrade: status %d code %s error %v", response.StatusCode, failure.Error.Code, err)
	}
}

func TestExpiryRejoinSpectatorAndFinishGame(t *testing.T) {
	h := newHarness(t)
	g := h.group("Alice", "Bob", "Cara")
	a, b, c := g.connections[1], g.connections[2], g.connections[3]
	g.start([]*connection{a, b, c}, a)
	g.place(g.falseBid())
	dealt := g.peek()
	a.disconnect()
	h.clock.Advance(room.ReconnectGrace)
	g.sync(g.observer.actor.view(g.m).Revision)
	withdrawn := findPlayer(g.observer.state.Game, a.actor.id)
	if !withdrawn.Withdrawn || withdrawn.HandCount != 1 || withdrawn.Hand != nil || *g.observer.state.Game.CurrentPlayerID != b.actor.id {
		t.Fatal("expiry failed to retain counted cards or skip only the departed player")
	}
	a.actor.cannotConnect(g.m, "ROOM_UNAVAILABLE")
	g.change(b, "challenge", struct{}{})
	result := g.observer.state.Game.LastResult
	want := make([]revealedHand, 0, 3)
	for _, p := range dealt.Game.Players {
		want = append(want, revealedHand{p.PlayerID, p.Hand})
	}
	if result.LoserID != a.actor.id || result.ClaimSatisfied || !reflect.DeepEqual(result.RevealedHands, want) || findPlayer(g.observer.state.Game, a.actor.id).RequiredHandSize != 1 {
		t.Fatal("departed bidder's claim/cards were lost or penalized again")
	}
	g.advance()
	returned := g.add(a.actor)
	if returned.state.Role != "spectator" || !findPlayer(returned.state.Game, a.actor.id).Withdrawn || findPlayer(returned.state.Game, a.actor.id).HandCount != 0 {
		t.Fatal("rejoin restored withdrawn participation")
	}
	requireError(t, returned.apply(returned.command("challenge", struct{}{})), "INACTIVE_PLAYER")
	g.loseUntilEliminated(b.actor.id)
	if got := g.observer.state; got.Game.Phase != "finished" || *got.Game.WinnerID != c.actor.id {
		t.Fatal("game did not finish after expiry and rejoin")
	}
}
