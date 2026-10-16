package room

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bkucharz/falsepoker/internal/game"
)

func readyCommand(r *Room, id string, revision uint64, ready bool) Command {
	c := Command{RequestID: id, RoomID: r.ID(), ExpectedRevision: revision, Action: "ready", Ready: ready}
	c.Fingerprint = sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%d/%t", id, r.ID(), revision, ready)))
	return c
}

func TestCommandRetryAdmissionAndRetention(t *testing.T) {
	r := createRoom(t, deterministicRegistry(), "admin")
	sub := subscribe(t, r, "admin")
	defer sub.Close()
	initial := receiveView(t, sub)
	now := time.Unix(100, 0)
	first := readyCommand(r, "first", initial.Revision, true)
	result := sub.Execute(first, now)
	if result.Err != nil || !result.Changed || result.Replayed || result.Revision != initial.Revision+1 {
		t.Fatalf("first: %+v", result)
	}
	receiveView(t, sub)
	replay := sub.Execute(first, now.Add(time.Minute))
	if replay.Err != nil || !replay.Changed || !replay.Replayed || replay.Revision != result.Revision {
		t.Fatalf("replay: %+v", replay)
	}
	assertNoUpdate(t, sub)
	conflict := first
	conflict.Fingerprint = sha256.Sum256([]byte("different"))
	if got := sub.Execute(conflict, now); !errors.Is(got.Err, ErrRequestIDReused) {
		t.Fatalf("conflict: %+v", got)
	}
	if got := sub.Execute(first, now.Add(CommandRetryLifetime)); !errors.Is(got.Err, ErrStaleRevision) || got.Replayed {
		t.Fatalf("retry extended expiry: %+v", got)
	}
	assertNoUpdate(t, sub)
}

func TestCommandGuardsAndCachedRejections(t *testing.T) {
	r := createRoom(t, deterministicRegistry(), "admin")
	sub := subscribe(t, r, "admin")
	defer sub.Close()
	initial := receiveView(t, sub)
	now := time.Unix(100, 0)
	tests := []struct {
		name   string
		change func(*Command)
		want   error
	}{
		{"room", func(c *Command) { c.RoomID = "old" }, ErrStaleRoom},
		{"game", func(c *Command) { c.Context.GameID = 1 }, ErrStaleGame},
		{"round", func(c *Command) { c.Context.RoundID = 1 }, ErrStaleRound},
		{"revision", func(c *Command) { c.ExpectedRevision = 0 }, ErrStaleRevision},
		{"no game", func(c *Command) { c.Action = "challenge" }, ErrNoGame},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := readyCommand(r, tt.name, initial.Revision, true)
			tt.change(&c)
			got := sub.Execute(c, now)
			if !errors.Is(got.Err, tt.want) || got.Changed || got.Revision != initial.Revision {
				t.Fatalf("result: %+v", got)
			}
			again := sub.Execute(c, now)
			if !errors.Is(again.Err, tt.want) || !again.Replayed {
				t.Fatalf("replay: %+v", again)
			}
			assertNoUpdate(t, sub)
		})
	}
	c := readyCommand(r, "noop", initial.Revision, false)
	if got := sub.Execute(c, now); got.Err != nil || got.Changed {
		t.Fatalf("no-op: %+v", got)
	}
	assertNoUpdate(t, sub)
	sub.Close()
	if got := sub.Execute(c, now); !errors.Is(got.Err, ErrSubscriptionClosed) {
		t.Fatalf("closed connection replayed: %+v", got)
	}
}

func TestCommandRetryCapacityNeverEvictsUnexpiredResults(t *testing.T) {
	r := createRoom(t, deterministicRegistry(), "admin")
	sub := subscribe(t, r, "admin")
	defer sub.Close()
	initial := receiveView(t, sub)
	now := time.Unix(100, 0)
	for i := 0; i < CommandRetryCapacity; i++ {
		c := readyCommand(r, fmt.Sprint(i), initial.Revision, false)
		if got := sub.Execute(c, now); got.Err != nil {
			t.Fatalf("record %d: %+v", i, got)
		}
	}
	change := readyCommand(r, "new", initial.Revision, true)
	got := sub.Execute(change, now.Add(time.Second))
	if !errors.Is(got.Err, ErrRetryWindowFull) || got.RetryAfter != CommandRetryLifetime-time.Second || got.Changed {
		t.Fatalf("full: %+v", got)
	}
	if got := sub.Execute(readyCommand(r, "0", initial.Revision, false), now.Add(time.Minute)); got.Err != nil || !got.Replayed {
		t.Fatalf("cached at capacity: %+v", got)
	}
	assertNoUpdate(t, sub)
	if got := sub.Execute(change, now.Add(CommandRetryLifetime)); got.Err != nil || !got.Changed {
		t.Fatalf("expired capacity: %+v", got)
	}
	receiveView(t, sub)
}

func TestConcurrentDuplicateCommandCommitsOnce(t *testing.T) {
	r := createRoom(t, deterministicRegistry(), "admin")
	subs := []*Subscription{subscribe(t, r, "admin"), subscribe(t, r, "admin")}
	for _, sub := range subs {
		defer sub.Close()
		receiveView(t, sub)
	}
	c := readyCommand(r, "same", roomView(t, r, "admin").Revision, true)
	results := make([]CommandResult, len(subs))
	var wg sync.WaitGroup
	for i, sub := range subs {
		wg.Add(1)
		go func(i int, sub *Subscription) { defer wg.Done(); results[i] = sub.Execute(c, time.Unix(100, 0)) }(i, sub)
	}
	wg.Wait()
	replays := 0
	for i, got := range results {
		if got.Err != nil || !got.Changed || got.Revision != c.ExpectedRevision+1 {
			t.Fatalf("result: %+v", got)
		}
		if got.Replayed {
			replays++
		}
		receiveView(t, subs[i])
		assertNoUpdate(t, subs[i])
	}
	if replays != 1 {
		t.Fatalf("replays=%d", replays)
	}
}

func TestLimitedSubscriptionIsAtomicAndReleasesCapacity(t *testing.T) {
	r := createRoom(t, deterministicRegistry(), "admin")
	if _, err := r.SubscribeLimited("unknown", "wrong", 2); !errors.Is(err, ErrNotMember) {
		t.Fatal(err)
	}
	if _, err := r.SubscribeLimited("admin", "wrong", 2); !errors.Is(err, ErrStaleRoom) {
		t.Fatal(err)
	}
	a, err := r.SubscribeLimited("admin", r.ID(), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := r.SubscribeLimited("admin", r.ID(), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := r.SubscribeLimited("admin", r.ID(), 2); !errors.Is(err, ErrConnectionLimit) {
		t.Fatal(err)
	}
	a.Close()
	c, err := r.SubscribeLimited("admin", r.ID(), 2)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestCommandRetrySurvivesGameReplay(t *testing.T) {
	r := createRoom(t, deterministicRegistry(), "admin")
	if err := r.Join("other", "Other"); err != nil {
		t.Fatal(err)
	}
	sub := subscribe(t, r, "admin")
	defer sub.Close()
	initial := receiveView(t, sub)
	now := time.Unix(100, 0)
	c := readyCommand(r, "before first game", initial.Revision, true)
	first := sub.Execute(c, now)
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	receiveView(t, sub)
	if err := r.SetReady("other", true); err != nil {
		t.Fatal(err)
	}
	receiveView(t, sub)
	options := StartOptions{SeatOrder: []game.PlayerID{"admin", "other"}, FirstBidder: "admin"}
	if err := r.Start("admin", options); err != nil {
		t.Fatal(err)
	}
	receiveView(t, sub)
	if err := r.Leave("other"); err != nil {
		t.Fatal(err)
	}
	receiveView(t, sub)
	if err := r.Join("other", "Other"); err != nil {
		t.Fatal(err)
	}
	receiveView(t, sub)
	for _, id := range []game.PlayerID{"admin", "other"} {
		if err := r.SetReady(id, true); err != nil {
			t.Fatal(err)
		}
		receiveView(t, sub)
	}
	if err := r.Start("admin", options); err != nil {
		t.Fatal(err)
	}
	secondGame := receiveView(t, sub)
	if secondGame.GameID != 2 {
		t.Fatal("second game not started")
	}
	got := sub.Execute(c, now.Add(time.Minute))
	if got.Err != nil || !got.Replayed || got.Revision != first.Revision {
		t.Fatalf("across-game retry: %+v", got)
	}
	assertNoUpdate(t, sub)
}

func TestDepartedPlayersCannotGrowRetryStorageWithoutBound(t *testing.T) {
	r := createRoom(t, deterministicRegistry(), "admin")
	// A small internal member budget keeps the churn scenario compact; public
	// registry configuration still requires room capacity for 24 participants.
	r.maxMembers = 2
	admin := subscribe(t, r, "admin")
	defer admin.Close()
	initial := receiveView(t, admin)
	now := time.Unix(100, 0)
	original := readyCommand(r, "original", initial.Revision, false)
	if got := admin.Execute(original, now); got.Err != nil {
		t.Fatal(got.Err)
	}
	for i := 0; i < 2; i++ {
		actor := game.PlayerID(fmt.Sprintf("visitor%d", i))
		if err := r.Join(actor, string(actor)); err != nil {
			t.Fatal(err)
		}
		receiveView(t, admin)
		sub := subscribe(t, r, actor)
		view := receiveView(t, sub)
		count := CommandRetryCapacity
		if i == 1 {
			count--
		} // admin holds the remaining record
		for j := 0; j < count; j++ {
			if got := sub.Execute(readyCommand(r, fmt.Sprint(j), view.Revision, false), now); got.Err != nil {
				t.Fatal(got.Err)
			}
		}
		if err := r.Leave(actor); err != nil {
			t.Fatal(err)
		}
		receiveView(t, admin)
	}
	change := readyCommand(r, "new intent", roomView(t, r, "admin").Revision, true)
	if got := admin.Execute(change, now); !errors.Is(got.Err, ErrRetryWindowFull) || got.Changed {
		t.Fatalf("departed records lost their bound: %+v", got)
	}
	if got := admin.Execute(original, now); got.Err != nil || !got.Replayed {
		t.Fatalf("unexpired outcome evicted: %+v", got)
	}
	assertNoUpdate(t, admin)
	if got := admin.Execute(change, now.Add(CommandRetryLifetime)); got.Err != nil || !got.Changed {
		t.Fatalf("expired churn records not released: %+v", got)
	}
	receiveView(t, admin)
}
