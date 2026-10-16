package room

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/bkucharz/falsepoker/internal/game"
)

func TestSubscriptionsReceiveOrderedLobbyAndGameViews(t *testing.T) {
	r := createRoom(t, deterministicRegistry(), "admin")
	sub := subscribe(t, r, "admin")
	defer sub.Close()
	initial := receiveView(t, sub)
	if initial.Revision != 1 || initial.Game != nil || !reflect.DeepEqual(initial, roomView(t, r, "admin")) {
		t.Fatal("initial subscription does not match the room's initial revision")
	}
	operations := []struct {
		name string
		act  func() error
	}{
		{"join p0", func() error { return r.Join("p0", "First") }},
		{"join p1", func() error { return r.Join("p1", "Second") }},
		{"ready p0", func() error { return r.SetReady("p0", true) }},
		{"ready p1", func() error { return r.SetReady("p1", true) }},
		{"start", func() error {
			return r.Start("admin", StartOptions{SeatOrder: []game.PlayerID{"p0", "p1"}, FirstBidder: "p0"})
		}},
		{"bid", func() error {
			return r.PlaceBid("p0", GameContext{GameID: 1, RoundID: 1}, game.Bid{Category: game.RoyalStraightFlush, Suit: game.Clubs})
		}},
		{"leave and finish", func() error { return r.Leave("p0") }},
	}
	for i, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.act(); err != nil {
				t.Fatal(err)
			}
			view := receiveView(t, sub)
			if view.Revision != uint64(i+2) || !reflect.DeepEqual(view, roomView(t, r, "admin")) || view.Revision != lobby(t, r, "admin").Revision {
				t.Fatalf("notification differs from committed revision: %+v", view)
			}
			assertNoUpdate(t, sub)
		})
	}
	if view := roomView(t, r, "admin"); view.Game.Winner != "p1" || view.Members[1].Ready {
		t.Fatal("departure notification did not include game end and readiness reset")
	}
	before := roomView(t, r, "admin")
	if err := r.SetReady("p1", false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, roomView(t, r, "admin")) {
		t.Fatal("idempotent readiness request changed the revision")
	}
	assertNoUpdate(t, sub)
	if err := r.Start("p1", StartOptions{ShuffleSeats: true}); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("nonadmin start error = %v", err)
	}
	assertNoUpdate(t, sub)
}

func TestConcurrentNotificationsHaveContiguousRevisions(t *testing.T) {
	r := createRoom(t, deterministicRegistry(), "admin")
	sub := subscribe(t, r, "admin")
	defer sub.Close()
	initial := receiveView(t, sub)
	// Leave room in the bounded queue for every accepted operation; consumers
	// should observe the order chosen by the room, not goroutine launch order.
	const joins = 12
	for _, err := range concurrent(joins, func(i int) error {
		if err := r.Join(game.PlayerID(fmt.Sprintf("p%d", i)), "Member"); err != nil {
			return err
		}
		_, err := r.View("admin")
		return err
	}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= joins; i++ {
		view := receiveView(t, sub)
		if view.Revision != initial.Revision+uint64(i) || len(view.Members) != i+1 {
			t.Fatalf("out-of-order revision or mixed snapshot: %+v", view)
		}
	}
	assertNoUpdate(t, sub)
}

func TestSubscribeCannotMissConcurrentChanges(t *testing.T) {
	for iteration := 0; iteration < 32; iteration++ {
		r := createRoom(t, deterministicRegistry(), "admin")
		var sub *Subscription
		for _, err := range concurrent(2, func(i int) error {
			if i == 0 {
				var err error
				sub, err = r.Subscribe("admin")
				return err
			}
			return r.Join("new", "New")
		}) {
			if err != nil {
				t.Fatal(err)
			}
		}
		first := receiveView(t, sub)
		if first.Revision == 1 {
			second := receiveView(t, sub)
			if len(first.Members) != 1 || second.Revision != 2 || len(second.Members) != 2 {
				t.Fatal("subscription missed or reordered a concurrent join")
			}
		} else if first.Revision != 2 || len(first.Members) != 2 {
			t.Fatal("subscription did not begin with a consistent current snapshot")
		}
		assertNoUpdate(t, sub)
		sub.Close()
	}
}

func TestSlowSubscriberDoesNotBlockOtherConnections(t *testing.T) {
	r := createRoom(t, deterministicRegistry(), "admin")
	slow, fast := subscribe(t, r, "admin"), subscribe(t, r, "admin")
	defer slow.Close()
	defer fast.Close()
	initial := receiveView(t, fast)
	finished := make(chan error, 1)
	go func() {
		for i := 0; i < SubscriberQueueCapacity+4; i++ {
			if err := r.SetReady("admin", i%2 == 0); err != nil {
				finished <- err
				return
			}
			view, ok := <-fast.Updates()
			if !ok || view.Revision != initial.Revision+uint64(i+1) {
				finished <- errors.New("slow connection disrupted another connection's revisions")
				return
			}
		}
		finished <- nil
	}()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("full subscriber queue blocked room operations")
	}
	assertSubscriptionClosed(t, slow, ErrSlowSubscriber)
	select {
	case <-fast.Done():
		t.Fatal("healthy connection was closed alongside the slow one")
	default:
	}
	if r.memberSeat("admin") == -1 {
		t.Fatal("overflow removed membership instead of just the connection")
	}
	reconnected := subscribe(t, r, "admin")
	defer reconnected.Close()
	if fresh := receiveView(t, reconnected); !reflect.DeepEqual(fresh, roomView(t, r, "admin")) {
		t.Fatal("fresh subscription failed to resynchronize after overflow")
	}
}

func TestSubscriptionLifecycle(t *testing.T) {
	tests := []struct {
		name string
		act  func(*Room, *Subscription) error
		want error
	}{
		{"manual close", func(_ *Room, sub *Subscription) error { sub.Close(); return nil }, nil},
		{"member departure", func(r *Room, _ *Subscription) error { return r.Leave("p0") }, ErrNotMember},
		{"room deletion", func(r *Room, _ *Subscription) error {
			if err := r.Leave("admin"); err != nil {
				return err
			}
			return r.Leave("p0")
		}, ErrRoomClosed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := readyRoom(t, 1)
			first, second := subscribe(t, r, "p0"), subscribe(t, r, "p0")
			defer second.Close()
			if err := tt.act(r, first); err != nil {
				t.Fatal(err)
			}
			assertSubscriptionClosed(t, first, tt.want)
			first.Close()
			assertSubscriptionClosed(t, first, tt.want)
			if tt.name == "manual close" {
				if view := receiveView(t, second); view.Recipient != "p0" || view.Revision != roomView(t, r, "p0").Revision {
					t.Fatal("closing one connection changed membership, revision, or another connection")
				}
			} else {
				assertSubscriptionClosed(t, second, tt.want)
			}
		})
	}
	r := createRoom(t, deterministicRegistry(), "admin")
	for _, actor := range []game.PlayerID{"", "outsider"} {
		if sub, err := r.Subscribe(actor); sub != nil || !errors.Is(err, ErrNotMember) {
			t.Fatalf("unauthorized subscription = %v, %v", sub, err)
		}
	}
	if err := r.Leave("admin"); err != nil {
		t.Fatal(err)
	}
	if sub, err := r.Subscribe("admin"); sub != nil || !errors.Is(err, ErrRoomClosed) {
		t.Fatalf("closed room subscription = %v, %v", sub, err)
	}
}

func TestNotificationsAreDetachedAndRecipientSpecific(t *testing.T) {
	r := startedViewRoom(t, 3)
	first, second, viewer := subscribe(t, r, "p1"), subscribe(t, r, "p1"), subscribe(t, r, "admin")
	defer first.Close()
	defer second.Close()
	defer viewer.Close()
	for _, sub := range []*Subscription{first, second, viewer} {
		initial := receiveView(t, sub)
		if sub == viewer {
			for _, player := range initial.Game.Players {
				if len(player.Hand) != 1 {
					t.Fatal("spectator notification omitted an authorized hand")
				}
			}
		} else if initial.Game.Players[0].Hand != nil || initial.Game.Players[2].Hand != nil || len(initial.Game.Players[1].Hand) != 1 {
			t.Fatal("participant notification leaked opponent's hand")
		}
	}
	resolveFalseBid(t, r)
	// Opening bid then resolution are separately committed and queued.
	for _, sub := range []*Subscription{first, second, viewer} {
		receiveView(t, sub)
	}
	mutated := receiveView(t, first)
	before := roomView(t, r, "p1")
	mutated.Members[0].DisplayName = "changed"
	mutated.Game.Players[1].Hand[0] = game.Card{}
	mutated.Game.LastBid.PlayerID = "changed"
	mutated.Game.LastResult.RevealedHands[0].Cards[0] = game.Card{}
	if other := receiveView(t, second); !reflect.DeepEqual(other, before) {
		t.Fatal("two connections for one member share mutable notification storage")
	}
	if other := receiveView(t, viewer); other.Game.LastBid.PlayerID == "changed" || other.Game.LastResult.RevealedHands[0].Cards[0] == (game.Card{}) || other.Game.Players[1].Hand[0] == (game.Card{}) {
		t.Fatal("different recipients share mutable notification storage")
	}
	if !reflect.DeepEqual(before, roomView(t, r, "p1")) {
		t.Fatal("queued view mutation affected authoritative state")
	}
	advanceRound(t, r)
	for _, sub := range []*Subscription{first, second} {
		view := receiveView(t, sub)
		if view.Game.Players[0].Hand != nil || view.Game.LastResult.RevealedHands[0].Cards[0] == (game.Card{}) {
			t.Fatal("new-round notification leaked a current hand or lost the public reveal")
		}
	}
}

func TestConcurrentSubscribeCloseReadAndPublish(t *testing.T) {
	r := createRoom(t, deterministicRegistry(), "admin")
	for _, err := range concurrent(96, func(i int) error {
		if i%3 == 0 {
			return r.Join(game.PlayerID(fmt.Sprintf("p%d", i)), "Member")
		}
		sub, err := r.Subscribe("admin")
		if err != nil {
			return err
		}
		defer sub.Close()
		select {
		case view, ok := <-sub.Updates():
			if ok {
				view.Members[0].DisplayName = "changed"
			}
		default:
		}
		if _, err := r.View("admin"); err != nil {
			return err
		}
		// Exercise closure and Err simultaneously with enqueues and overflow.
		for _, err := range concurrent(2, func(int) error { sub.Close(); return sub.Err() }) {
			if err != nil && !errors.Is(err, ErrSlowSubscriber) {
				return err
			}
		}
		return nil
	}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	view := roomView(t, r, "admin")
	if len(view.Members) != 33 || view.Members[0].DisplayName != "admin" {
		t.Fatal("concurrent subscriptions changed authoritative membership")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.subscribers) != 0 {
		t.Fatal("closed subscriptions remain registered")
	}
}

func TestSubscriptionRolesChangeOnEliminationAndReplay(t *testing.T) {
	t.Run("eliminated participant becomes spectator", func(t *testing.T) {
		r := startedViewRoom(t, 13)
		sub := subscribe(t, r, "p0")
		defer sub.Close()
		if initial := receiveView(t, sub); initial.Role != Participant || initial.Game.Players[1].Hand != nil {
			t.Fatal("initial participant view has incorrect access")
		}
		resolveFalseBid(t, r)
		receiveView(t, sub) // opening bid
		if resolved := receiveView(t, sub); resolved.Role != Spectator || !resolved.Game.Players[0].Eliminated {
			t.Fatal("elimination notification retained participant access")
		}
		advanceRound(t, r)
		view := receiveView(t, sub)
		if view.Role != Spectator || view.Game.Players[0].Hand != nil {
			t.Fatal("eliminated spectator received a new hand")
		}
		for _, player := range view.Game.Players[1:] {
			if len(player.Hand) != 1 {
				t.Fatal("eliminated spectator cannot inspect survivor hands")
			}
		}
	})
	t.Run("spectator participates in replay", func(t *testing.T) {
		r := startedViewRoom(t, 2)
		sub := subscribe(t, r, "admin")
		defer sub.Close()
		initial := receiveView(t, sub)
		if initial.Role != Spectator || len(initial.Game.Players[1].Hand) != 1 {
			t.Fatal("initial spectator cannot inspect survivors")
		}
		if err := r.Leave("p0"); err != nil {
			t.Fatal(err)
		}
		receiveView(t, sub)
		for _, actor := range []game.PlayerID{"admin", "p1"} {
			if err := r.SetReady(actor, true); err != nil {
				t.Fatal(err)
			}
			receiveView(t, sub)
		}
		if err := r.Start("admin", StartOptions{SeatOrder: []game.PlayerID{"admin", "p1"}, FirstBidder: "admin"}); err != nil {
			t.Fatal(err)
		}
		view := receiveView(t, sub)
		if view.Role != Participant || view.GameID != 2 || view.Game.LastResult != nil || view.Game.Players[1].Hand != nil || len(view.Game.Players[0].Hand) != 1 || view.Revision <= initial.Revision {
			t.Fatal("replay notification retained spectator access, old result, or old revision")
		}
	})
}

func subscribe(t *testing.T, r *Room, actor game.PlayerID) *Subscription {
	t.Helper()
	sub, err := r.Subscribe(actor)
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func receiveView(t *testing.T, sub *Subscription) RoomView {
	t.Helper()
	select {
	case view, ok := <-sub.Updates():
		if !ok {
			t.Fatalf("subscription unexpectedly closed: %v", sub.Err())
		}
		return view
	case <-time.After(2 * time.Second):
		t.Fatal("notification was not queued")
		return RoomView{}
	}
}

func assertNoUpdate(t *testing.T, sub *Subscription) {
	t.Helper()
	select {
	case view, ok := <-sub.Updates():
		t.Fatalf("unexpected update or closure: %+v, open=%v", view, ok)
	default:
	}
}

func assertSubscriptionClosed(t *testing.T, sub *Subscription, want error) {
	t.Helper()
	select {
	case <-sub.Done():
	default:
		t.Fatal("subscription did not signal closure")
	}
	if err := sub.Err(); !errors.Is(err, want) {
		t.Fatalf("subscription error = %v; want %v", err, want)
	}
	if _, ok := <-sub.Updates(); ok {
		t.Fatal("closed subscription retained queued views")
	}
}
