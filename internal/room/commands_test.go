package room

import (
	"errors"
	"reflect"
	"testing"

	"github.com/bkucharz/falsepoker/internal/game"
)

func TestGameCommandErrorsAreAtomic(t *testing.T) {
	r := startedViewRoom(t, 3)
	context := gameContext(roomView(t, r, "p0"))
	low := game.Bid{Category: game.HighCard, PrimaryRank: game.Nine}
	tests := []struct {
		name string
		act  func() error
		want error
	}{
		{"unknown bid actor", func() error { return r.PlaceBid("unknown", context, low) }, ErrNotMember},
		{"spectator bid", func() error { return r.PlaceBid("admin", context, low) }, game.ErrUnknownPlayer},
		{"out of turn bid", func() error { return r.PlaceBid("p1", context, low) }, game.ErrOutOfTurn},
		{"invalid opening", func() error { return r.PlaceBid("p0", context, game.Bid{}) }, game.ErrInvalidBid},
		{"no preceding bid", func() error { return r.Challenge("p0", context) }, game.ErrNoBid},
		{"out of turn challenge", func() error { return r.Challenge("p1", context) }, game.ErrOutOfTurn},
		{"spectator challenge", func() error { return r.Challenge("admin", context) }, game.ErrUnknownPlayer},
		{"unknown challenge actor", func() error { return r.Challenge("unknown", context) }, ErrNotMember},
		{"premature next round", func() error { return r.NextRound("p0", context) }, game.ErrWrongPhase},
		{"unknown next-round actor", func() error { return r.NextRound("unknown", context) }, ErrNotMember},
		{"missing context", func() error { return r.PlaceBid("p0", GameContext{}, low) }, ErrStaleContext},
		{"wrong game", func() error { return r.Challenge("p0", GameContext{GameID: 2, RoundID: 1}) }, ErrStaleContext},
		{"wrong round", func() error { return r.NextRound("p0", GameContext{GameID: 1, RoundID: 2}) }, ErrStaleContext},
	}
	sub := subscribe(t, r, "admin")
	defer sub.Close()
	receiveView(t, sub)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := roomView(t, r, "admin")
			if err := tt.act(); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v; want %v", err, tt.want)
			}
			if !reflect.DeepEqual(before, roomView(t, r, "admin")) {
				t.Fatal("rejected command changed state or revision")
			}
			assertNoUpdate(t, sub)
		})
	}
	if err := r.PlaceBid("p0", context, low); err != nil {
		t.Fatal(err)
	}
	receiveView(t, sub)
	before := roomView(t, r, "admin")
	if err := r.PlaceBid("p1", context, low); !errors.Is(err, game.ErrBidNotHigher) {
		t.Fatalf("equal bid error = %v", err)
	}
	if !reflect.DeepEqual(before, roomView(t, r, "admin")) {
		t.Fatal("equal raise changed state or revision")
	}
	assertNoUpdate(t, sub)
}

func TestCommandsWithoutGameAndAfterRoomDeletion(t *testing.T) {
	r := createRoom(t, deterministicRegistry(), "admin")
	for _, command := range []func() error{
		func() error { return r.PlaceBid("admin", GameContext{}, game.Bid{}) },
		func() error { return r.Challenge("admin", GameContext{}) },
		func() error { return r.NextRound("admin", GameContext{}) },
	} {
		if err := command(); !errors.Is(err, ErrNoGame) {
			t.Fatalf("lobby command error = %v; want ErrNoGame", err)
		}
	}
	if err := r.Leave("admin"); err != nil {
		t.Fatal(err)
	}
	for _, command := range []func() error{
		func() error { return r.PlaceBid("admin", GameContext{}, game.Bid{}) },
		func() error { return r.Challenge("admin", GameContext{}) },
		func() error { return r.NextRound("admin", GameContext{}) },
	} {
		if err := command(); !errors.Is(err, ErrRoomClosed) {
			t.Fatalf("closed room command error = %v", err)
		}
	}
}

func TestRoomCommandsCompleteGameAndRejectOldContext(t *testing.T) {
	r := startedViewRoom(t, 2)
	initial := roomView(t, r, "admin")
	old := gameContext(initial)
	sub := subscribe(t, r, "admin")
	defer sub.Close()
	revision := receiveView(t, sub).Revision
	for round := uint64(1); round <= 5; round++ {
		view := roomView(t, r, "p0")
		context := gameContext(view)
		bid, err := falseRoyalBid(engineState(r))
		if err != nil {
			t.Fatal(err)
		}
		if err := r.PlaceBid("p0", context, bid); err != nil {
			t.Fatal(err)
		}
		placed := receiveView(t, sub)
		revision++
		if placed.Revision != revision || placed.Game.CurrentPlayer != "p1" || placed.Game.LastBid.Bid != bid {
			t.Fatal("bid notification omitted committed state or revision")
		}
		if err := r.Challenge("p1", context); err != nil {
			t.Fatal(err)
		}
		resolved := receiveView(t, sub)
		revision++
		if resolved.Revision != revision || resolved.Game.LastResult.RoundID != round || resolved.Game.LastResult.Loser != "p0" {
			t.Fatal("challenge notification omitted result or revision")
		}
		if round < 5 {
			before := roomView(t, r, "admin")
			if err := r.NextRound("admin", context); !errors.Is(err, game.ErrUnknownPlayer) {
				t.Fatalf("spectator next-round error = %v", err)
			}
			if err := r.NextRound("p1", context); !errors.Is(err, game.ErrOutOfTurn) {
				t.Fatalf("wrong next starter error = %v", err)
			}
			if !reflect.DeepEqual(before, roomView(t, r, "admin")) {
				t.Fatal("unauthorized advancement changed state or revision")
			}
			assertNoUpdate(t, sub)
			if err := r.NextRound("p0", context); err != nil {
				t.Fatal(err)
			}
			advanced := receiveView(t, sub)
			revision++
			if advanced.Revision != revision || advanced.Game.RoundID != round+1 || advanced.Game.CurrentPlayer != "p0" || advanced.Game.LastResult.RoundID != round {
				t.Fatal("next-round notification lost context or prior reveal")
			}
			if err := r.PlaceBid("p0", context, bid); !errors.Is(err, ErrStaleContext) {
				t.Fatalf("old round command error = %v", err)
			}
		} else {
			if resolved.Game.Phase != game.GameFinished || resolved.Game.Winner != "p1" {
				t.Fatal("room API did not complete the game")
			}
			for _, member := range resolved.Members {
				if member.Ready {
					t.Fatal("terminal challenge published stale readiness")
				}
			}
			for _, act := range []func() error{
				func() error { return r.PlaceBid("p1", context, bid) },
				func() error { return r.Challenge("p1", context) },
				func() error { return r.NextRound("p1", context) },
			} {
				if err := act(); !errors.Is(err, game.ErrWrongPhase) {
					t.Fatalf("finished command error = %v", err)
				}
			}
		}
		assertNoUpdate(t, sub)
	}
	for _, actor := range []game.PlayerID{"p0", "p1"} {
		if err := r.SetReady(actor, true); err != nil {
			t.Fatal(err)
		}
		receiveView(t, sub)
	}
	if err := r.Start("admin", StartOptions{SeatOrder: []game.PlayerID{"p0", "p1"}, FirstBidder: "p0"}); err != nil {
		t.Fatal(err)
	}
	replayed := receiveView(t, sub)
	if replayed.GameID != 2 || replayed.Game.RoundID != 1 || replayed.Game.LastResult != nil {
		t.Fatal("replay notification retained old game context")
	}
	before := roomView(t, r, "admin")
	if err := r.PlaceBid("p0", old, game.Bid{Category: game.HighCard, PrimaryRank: game.Nine}); !errors.Is(err, ErrStaleContext) {
		t.Fatalf("old game command error = %v", err)
	}
	if !reflect.DeepEqual(before, roomView(t, r, "admin")) {
		t.Fatal("old-game command affected replay")
	}
	assertNoUpdate(t, sub)
}

func TestConcurrentChallengeResolvesOnce(t *testing.T) {
	r := startedViewRoom(t, 3)
	context := gameContext(roomView(t, r, "p0"))
	if err := r.PlaceBid("p0", context, game.Bid{Category: game.RoyalStraightFlush, Suit: game.Clubs}); err != nil {
		t.Fatal(err)
	}
	sub := subscribe(t, r, "admin")
	defer sub.Close()
	before := receiveView(t, sub)
	accepted := 0
	for _, err := range concurrent(64, func(int) error { return r.Challenge("p1", context) }) {
		if err == nil {
			accepted++
		} else if !errors.Is(err, game.ErrWrongPhase) {
			t.Fatal(err)
		}
	}
	after := receiveView(t, sub)
	if accepted != 1 || after.Revision != before.Revision+1 || after.Game.LastResult.Loser != "p0" || after.Game.Players[0].RequiredHandSize != 2 {
		t.Fatal("concurrent challenges resolved or penalized more than once")
	}
	assertNoUpdate(t, sub)
	accepted = 0
	for _, err := range concurrent(32, func(int) error { return r.NextRound("p0", context) }) {
		if err == nil {
			accepted++
		} else if !errors.Is(err, ErrStaleContext) {
			t.Fatal(err)
		}
	}
	advanced := receiveView(t, sub)
	if accepted != 1 || advanced.Revision != after.Revision+1 || advanced.Game.RoundID != 2 {
		t.Fatal("concurrent advancements redealt more than once")
	}
	assertNoUpdate(t, sub)
}

func TestConcurrentConflictingCommandsUseCommittedState(t *testing.T) {
	r := startedViewRoom(t, 3)
	context := gameContext(roomView(t, r, "p0"))
	if err := r.PlaceBid("p0", context, game.Bid{Category: game.HighCard, PrimaryRank: game.Nine}); err != nil {
		t.Fatal(err)
	}
	before := roomView(t, r, "admin")
	accepted := 0
	for _, err := range concurrent(64, func(i int) error {
		if i%2 == 0 {
			return r.Challenge("p1", context)
		}
		return r.PlaceBid("p1", context, game.Bid{Category: game.HighCard, PrimaryRank: game.Ten})
	}) {
		if err == nil {
			accepted++
		} else if !errors.Is(err, game.ErrWrongPhase) && !errors.Is(err, game.ErrOutOfTurn) {
			t.Fatal(err)
		}
	}
	after := roomView(t, r, "admin")
	if accepted != 1 || after.Revision != before.Revision+1 {
		t.Fatal("conflicting commands were accepted against the same prior state")
	}
	if after.Game.RoundPhase == game.RoundBidding {
		if after.Game.LastBid.PlayerID != "p1" || after.Game.CurrentPlayer != "p2" || after.Game.LastResult != nil {
			t.Fatal("raise winner left inconsistent state")
		}
	} else if after.Game.LastResult.Challenger != "p1" || after.Game.LastResult.ChallengedBid.PlayerID != "p0" {
		t.Fatal("challenge winner left inconsistent state")
	}
}

func gameContext(view RoomView) GameContext {
	return GameContext{GameID: view.GameID, RoundID: view.Game.RoundID}
}
