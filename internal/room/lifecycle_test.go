package room

import (
	"crypto/sha256"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/bkucharz/falsepoker/internal/game"
	"github.com/bkucharz/falsepoker/internal/testclock"
)

type lifecycleClock struct{ *testclock.Clock }

func (c lifecycleClock) AfterFunc(d time.Duration, callback func()) Timer {
	return c.Clock.AfterFunc(d, callback)
}

func timedRegistry(t *testing.T) (*Registry, lifecycleClock) {
	t.Helper()
	clock := lifecycleClock{testclock.New()}
	registry := deterministicRegistry()
	if err := registry.EnableReconnectGrace(clock); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Close)
	return registry, clock
}

func TestInitialAttachmentDeadline(t *testing.T) {
	for _, tt := range []struct {
		name     string
		elapsed  time.Duration
		accepted bool
	}{
		{"before", ReconnectGrace - time.Nanosecond, true}, {"exact", ReconnectGrace, false}, {"after", ReconnectGrace + time.Nanosecond, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			registry, clock := timedRegistry(t)
			r := createRoom(t, registry, "admin")
			clock.Elapse(tt.elapsed) // callback intentionally delayed
			sub, err := r.Subscribe("admin")
			if tt.accepted {
				if err != nil {
					t.Fatal(err)
				}
				defer sub.Close()
				if receiveView(t, sub).Revision != 1 {
					t.Fatal("attachment changed revision")
				}
				clock.Advance(2 * time.Hour)
				if _, err := r.Membership("admin"); err != nil {
					t.Fatal("connected member expired")
				}
			} else {
				if !errors.Is(err, ErrRoomClosed) {
					t.Fatalf("expired attachment: %v", err)
				}
				if _, err := registry.Lookup(r.Code()); !errors.Is(err, ErrRoomNotFound) {
					t.Fatal("empty room retained")
				}
			}
		})
	}
}

func TestRepeatedHTTPStyleReadsAndJoinsDoNotExtendGrace(t *testing.T) {
	registry, clock := timedRegistry(t)
	r := createRoom(t, registry, "admin")
	clock.Advance(59 * time.Second)
	if _, _, err := r.JoinWithMembership("admin", "Renamed"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.View("admin"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	if _, err := r.View("admin"); !errors.Is(err, ErrRoomClosed) {
		t.Fatalf("read/join prolonged deadline: %v", err)
	}
	if clock.Pending() != 0 {
		t.Fatal("deleted room retained timers")
	}
}

func TestMultipleConnectionsAndFreshReconnectSnapshot(t *testing.T) {
	registry, clock := timedRegistry(t)
	r := createRoom(t, registry, "admin")
	if err := r.Join("other", "Other"); err != nil {
		t.Fatal(err)
	}
	a := subscribe(t, r, "admin")
	b := subscribe(t, r, "admin")
	other := subscribe(t, r, "other")
	defer other.Close()
	receiveView(t, a)
	receiveView(t, b)
	receiveView(t, other)
	for _, actor := range []game.PlayerID{"admin", "other"} {
		if err := r.SetReady(actor, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Start("admin", StartOptions{SeatOrder: []game.PlayerID{"admin", "other"}, FirstBidder: "admin"}); err != nil {
		t.Fatal(err)
	}
	before := roomView(t, r, "admin")
	a.Close()
	clock.Advance(2 * ReconnectGrace)
	if got := roomView(t, r, "admin"); !reflect.DeepEqual(got, before) {
		t.Fatal("closing one tab changed state")
	}
	b.Close()
	clock.Advance(ReconnectGrace - time.Nanosecond)
	if got := roomView(t, r, "admin"); !reflect.DeepEqual(got, before) {
		t.Fatal("grace changed the seat, hand, admin or turn")
	}
	reconnected := subscribe(t, r, "admin")
	defer reconnected.Close()
	if got := receiveView(t, reconnected); !reflect.DeepEqual(got, before) {
		t.Fatal("reconnect did not restore full view")
	}
	clock.Advance(2 * ReconnectGrace)
	if _, err := r.Membership("admin"); err != nil {
		t.Fatal("reconnected member expired")
	}
}

func TestOldTimerCannotRemoveReconnectedOrRejoinedMember(t *testing.T) {
	for _, rejoin := range []bool{false, true} {
		t.Run(map[bool]string{false: "reconnected", true: "new membership"}[rejoin], func(t *testing.T) {
			registry, clock := timedRegistry(t)
			r := createRoom(t, registry, "admin")
			if err := r.Join("other", "Other"); err != nil {
				t.Fatal(err)
			}
			other := subscribe(t, r, "other")
			defer other.Close()
			receiveView(t, other)
			sub := subscribe(t, r, "admin")
			receiveView(t, sub)
			sub.Close()
			clock.Elapse(59 * time.Second)
			old := clock.Take(true) // model an already-dispatched callback awaiting room.mu
			if len(old) != 1 {
				t.Fatal("missing old timer")
			}
			runOld := sync.OnceFunc(old[0])
			defer runOld()
			if rejoin {
				if err := r.Leave("admin"); err != nil {
					t.Fatal(err)
				}
				if err := r.Join("admin", "Again"); err != nil {
					t.Fatal(err)
				}
			}
			reconnected := subscribe(t, r, "admin")
			receiveView(t, reconnected)
			reconnected.Close() // a new grace generation
			clock.Elapse(time.Second)
			runOld()
			if _, err := r.Membership("admin"); err != nil {
				t.Fatal("old callback removed a newer generation")
			}
			clock.Advance(59 * time.Second)
			if _, err := r.Membership("admin"); !errors.Is(err, ErrNotMember) {
				t.Fatalf("new deadline did not expire: %v", err)
			}
		})
	}
}

func TestEarlyTimerCannotWithdrawMember(t *testing.T) {
	registry, clock := timedRegistry(t)
	r := createRoom(t, registry, "admin")
	callbacks := clock.Take(true)
	callbacks[0]()
	if _, err := r.Membership("admin"); err != nil {
		t.Fatal("early callback withdrew member")
	}
	if clock.Pending() != 1 {
		t.Fatal("early timer was not rescheduled")
	}
	clock.Advance(ReconnectGrace)
	if _, err := registry.Lookup(r.Code()); !errors.Is(err, ErrRoomNotFound) {
		t.Fatal("rescheduled timer did not remove empty room")
	}
}

func TestExpiryUsesDepartureRulesAndRetainsChallengeableBid(t *testing.T) {
	registry, clock := timedRegistry(t)
	r := createRoom(t, registry, "admin")
	subs := make(map[game.PlayerID]*Subscription)
	for _, actor := range []game.PlayerID{"admin", "b", "c"} {
		if actor != "admin" {
			if err := r.Join(actor, string(actor)); err != nil {
				t.Fatal(err)
			}
		}
		subs[actor] = subscribe(t, r, actor)
		defer subs[actor].Close()
		if err := r.SetReady(actor, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Start("admin", StartOptions{SeatOrder: []game.PlayerID{"admin", "b", "c"}, FirstBidder: "admin"}); err != nil {
		t.Fatal(err)
	}
	initial := roomView(t, r, "admin")
	context := gameContext(initial)
	if err := r.PlaceBid("admin", context, game.Bid{Category: game.RoyalStraightFlush, Suit: game.Clubs}); err != nil {
		t.Fatal(err)
	}
	subs["admin"].Close()
	clock.Advance(ReconnectGrace)
	view := roomView(t, r, "b")
	if view.Admin == "admin" || !view.Game.Players[0].Withdrawn || view.Game.Players[0].HandCount != 1 || view.Game.EliminationLimit != initial.Game.EliminationLimit || view.Game.CurrentPlayer != "b" {
		t.Fatal("expiry did not follow ordinary departure")
	}
	if err := r.Challenge("b", context); err != nil {
		t.Fatal(err)
	}
	view = roomView(t, r, "b")
	if view.Game.LastResult.Loser != "admin" || view.Game.NextStarter != "b" || len(view.Game.LastResult.RevealedHands) != 3 {
		t.Fatal("departed bid/cards were not retained")
	}
	if err := r.NextRound("b", context); err != nil {
		t.Fatal(err)
	}
	if got := roomView(t, r, "b"); got.Game.Players[0].HandCount != 0 {
		t.Fatal("expired player received new cards")
	}
	subs["c"].Close()
	clock.Advance(ReconnectGrace)
	if got := roomView(t, r, "b"); got.Game.Phase != game.GameFinished || got.Game.Winner != "b" {
		t.Fatal("expiry did not award the remaining participant a win")
	}
	clock.Advance(24 * time.Hour)
	if got := roomView(t, r, "b"); got.Game.Phase != game.GameFinished || got.Game.Winner != "b" {
		t.Fatal("occupied finished room expired through inactivity")
	}
}

func TestExpiryRacingWithReconnect(t *testing.T) {
	for i := 0; i < 40; i++ {
		registry, clock := timedRegistry(t)
		r := createRoom(t, registry, "admin")
		sub := subscribe(t, r, "admin")
		receiveView(t, sub)
		sub.Close()
		clock.Elapse(ReconnectGrace - time.Nanosecond)
		var reconnected *Subscription
		var err error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); clock.Advance(time.Nanosecond) }()
		go func() { defer wg.Done(); reconnected, err = r.Subscribe("admin") }()
		wg.Wait()
		if err == nil {
			receiveView(t, reconnected)
			if _, err := r.Membership("admin"); err != nil {
				t.Fatal("expiry won after successful reconnect")
			}
			reconnected.Close()
		} else if !errors.Is(err, ErrRoomClosed) {
			t.Fatal(err)
		}
		registry.Close()
	}
}

func TestRegistryShutdownCancelsAndJoinsTimers(t *testing.T) {
	registry, clock := timedRegistry(t)
	r := createRoom(t, registry, "admin")
	if err := r.Join("other", "Other"); err != nil {
		t.Fatal(err)
	}
	sub := subscribe(t, r, "admin")
	receiveView(t, sub)
	registry.Close()
	registry.Close()
	assertSubscriptionClosed(t, sub, ErrRoomClosed)
	if clock.Pending() != 0 {
		t.Fatal("shutdown retained timers")
	}
	if _, err := registry.Create("new", "New"); !errors.Is(err, ErrRoomClosed) {
		t.Fatal("closed registry created room")
	}
	clock.Advance(2 * ReconnectGrace)
	if _, err := r.View("admin"); !errors.Is(err, ErrRoomClosed) {
		t.Fatal("closed room reopened")
	}
}

func TestExpiryRacingWithGameCommand(t *testing.T) {
	registry, clock := timedRegistry(t)
	r := createRoom(t, registry, "admin")
	subs := make(map[game.PlayerID]*Subscription)
	for _, actor := range []game.PlayerID{"admin", "b", "c"} {
		if actor != "admin" {
			if err := r.Join(actor, string(actor)); err != nil {
				t.Fatal(err)
			}
		}
		subs[actor] = subscribe(t, r, actor)
		defer subs[actor].Close()
		if err := r.SetReady(actor, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Start("admin", StartOptions{SeatOrder: []game.PlayerID{"admin", "b", "c"}, FirstBidder: "admin"}); err != nil {
		t.Fatal(err)
	}
	context := gameContext(roomView(t, r, "b"))
	if err := r.PlaceBid("admin", context, game.Bid{Category: game.RoyalStraightFlush, Suit: game.Clubs}); err != nil {
		t.Fatal(err)
	}
	before := roomView(t, r, "b")
	subs["admin"].Close()
	clock.Elapse(ReconnectGrace)
	callbacks := clock.Take(false)
	if len(callbacks) != 1 {
		t.Fatal("missing dispatched timer")
	}
	command := Command{RequestID: "challenge", Fingerprint: sha256.Sum256([]byte("challenge")), RoomID: r.ID(), ExpectedRevision: before.Revision, Context: context, Action: "challenge"}
	var result CommandResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); callbacks[0]() }()
	go func() { defer wg.Done(); result = subs["b"].Execute(command, clock.Now()) }()
	wg.Wait()
	if !errors.Is(result.Err, ErrStaleRevision) || result.Changed || result.Revision != before.Revision+1 {
		t.Fatalf("deadline race: %+v", result)
	}
	view := roomView(t, r, "b")
	if !view.Game.Players[0].Withdrawn || view.Game.LastResult != nil || view.Game.CurrentPlayer != "b" {
		t.Fatal("expiry/challenge partially applied")
	}
	command.RequestID = "reviewed challenge"
	command.Fingerprint = sha256.Sum256([]byte(command.RequestID))
	command.ExpectedRevision = view.Revision
	if got := subs["b"].Execute(command, clock.Now()); got.Err != nil || !got.Changed || got.Revision != before.Revision+2 {
		t.Fatalf("reviewed challenge: %+v", got)
	}
	if got := roomView(t, r, "b"); got.Game.LastResult.Loser != "admin" || got.Game.Players[0].RequiredHandSize != 1 {
		t.Fatal("expiry repeated or departed player penalized")
	}
}

func TestSlowSubscriptionStartsGraceOnlyAfterFinalTabCloses(t *testing.T) {
	registry, clock := timedRegistry(t)
	r := createRoom(t, registry, "admin")
	healthy := subscribe(t, r, "admin")
	receiveView(t, healthy)
	slow := subscribe(t, r, "admin") // intentionally retain its initial queued view
	for i := 0; i < SubscriberQueueCapacity; i++ {
		if err := r.SetReady("admin", i%2 == 0); err != nil {
			t.Fatal(err)
		}
		receiveView(t, healthy)
	}
	assertSubscriptionClosed(t, slow, ErrSlowSubscriber)
	if clock.Pending() != 0 {
		t.Fatal("slow tab disconnected a player with another live tab")
	}
	healthy.Close()
	if clock.Pending() != 1 {
		t.Fatal("last tab did not start grace")
	}
	clock.Advance(ReconnectGrace)
	if _, err := registry.Lookup(r.Code()); !errors.Is(err, ErrRoomNotFound) {
		t.Fatal("slow consumer membership never expired")
	}
}

func TestOldRoomTimerCannotDeleteReusedCode(t *testing.T) {
	registry, clock := timedRegistry(t)
	registry.newCode = func() (string, error) { return "same", nil }
	old := createRoom(t, registry, "old")
	clock.Elapse(ReconnectGrace)
	callbacks := clock.Take(false)
	if len(callbacks) != 1 {
		t.Fatal("missing timer")
	}
	runOld := sync.OnceFunc(callbacks[0])
	defer runOld()
	if _, err := old.Membership("old"); !errors.Is(err, ErrRoomClosed) {
		t.Fatal(err)
	}
	fresh := createRoom(t, registry, "new")
	runOld()
	if found, err := registry.Lookup("same"); err != nil || found != fresh || fresh.ID() == old.ID() {
		t.Fatal("old timer affected reused invitation code")
	}
}

func TestNextStarterWaitsForGraceThenPassesInSeatOrder(t *testing.T) {
	registry, clock := timedRegistry(t)
	r := createRoom(t, registry, "admin")
	subs := make(map[game.PlayerID]*Subscription)
	for _, actor := range []game.PlayerID{"admin", "b", "c"} {
		if actor != "admin" {
			if err := r.Join(actor, string(actor)); err != nil {
				t.Fatal(err)
			}
		}
		subs[actor] = subscribe(t, r, actor)
		defer subs[actor].Close()
		if err := r.SetReady(actor, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Start("admin", StartOptions{SeatOrder: []game.PlayerID{"admin", "b", "c"}, FirstBidder: "admin"}); err != nil {
		t.Fatal(err)
	}
	context := gameContext(roomView(t, r, "b"))
	if err := r.PlaceBid("admin", context, game.Bid{Category: game.RoyalStraightFlush, Suit: game.Clubs}); err != nil {
		t.Fatal(err)
	}
	if err := r.Challenge("b", context); err != nil {
		t.Fatal(err)
	}
	subs["admin"].Close()
	clock.Advance(ReconnectGrace - time.Nanosecond)
	if view := roomView(t, r, "b"); view.Game.NextStarter != "admin" || view.Game.RoundID != context.RoundID {
		t.Fatal("next starter changed before grace expired")
	}
	clock.Advance(time.Nanosecond)
	if view := roomView(t, r, "b"); view.Game.NextStarter != "b" || view.Game.LastResult == nil || view.Game.RoundID != context.RoundID {
		t.Fatal("expiry lost resolution or failed to transfer next starter")
	}
	if err := r.NextRound("b", context); err != nil {
		t.Fatal(err)
	}
	if view := roomView(t, r, "b"); view.Game.RoundID != context.RoundID+1 || view.Game.CurrentPlayer != "b" || view.Game.Players[0].HandCount != 0 {
		t.Fatal("next round restored a departed player")
	}
}

func TestShutdownWaitsForAlreadyDispatchedTimer(t *testing.T) {
	registry, clock := timedRegistry(t)
	r := createRoom(t, registry, "admin")
	sub := subscribe(t, r, "admin")
	receiveView(t, sub)
	if err := r.Join("other", "Other"); err != nil {
		t.Fatal(err)
	}
	clock.Elapse(ReconnectGrace)
	callbacks := clock.Take(false)
	if len(callbacks) != 1 {
		t.Fatal("missing dispatched callback")
	}
	run := sync.OnceFunc(callbacks[0])
	defer run()
	closed := make(chan struct{})
	go func() { registry.Close(); close(closed) }()
	select {
	case <-sub.Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked holding room lock")
	}
	select {
	case <-closed:
		t.Fatal("shutdown returned before dispatched timer completed")
	default:
	}
	run()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("shutdown failed to join dispatched timer")
	}
}

func TestLifecycleRequiresExclusiveUnusedRegistry(t *testing.T) {
	for _, tt := range []struct {
		name    string
		prepare func(*Registry, lifecycleClock)
	}{
		{"already enabled", func(r *Registry, c lifecycleClock) {
			if err := r.EnableReconnectGrace(c); err != nil {
				t.Fatal(err)
			}
		}},
		{"occupied", func(r *Registry, _ lifecycleClock) { createRoom(t, r, "admin") }},
		{"closed", func(r *Registry, _ lifecycleClock) { r.Close() }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := deterministicRegistry()
			defer r.Close()
			clock := lifecycleClock{testclock.New()}
			tt.prepare(r, clock)
			if err := r.EnableReconnectGrace(clock); err == nil {
				t.Fatal("lifecycle enabled on an owned/occupied/closed registry")
			}
		})
	}
	r := deterministicRegistry()
	defer r.Close()
	if err := r.EnableReconnectGrace(nil); err == nil {
		t.Fatal("nil lifecycle clock accepted")
	}
}
