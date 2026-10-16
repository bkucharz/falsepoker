package game_test

import (
	"errors"
	"math/rand"
	"reflect"
	"slices"
	"testing"

	"github.com/bkucharz/falsepoker/internal/game"
)

func TestWithdrawDuringBidding(t *testing.T) {
	tests := []struct {
		name      string
		starter   game.PlayerID
		departing game.PlayerID
		current   game.PlayerID
		afterBid  game.PlayerID
	}{
		{"current player", "a", "a", "b", "c"},
		{"current player wraps", "c", "c", "a", "b"},
		{"future turn skipped", "a", "b", "a", "c"},
		{"other future player", "a", "c", "a", "b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newTestGame(t, []game.PlayerID{"a", "b", "c"}, tt.starter)
			before := g.State()
			if err := g.Withdraw(tt.departing); err != nil {
				t.Fatal(err)
			}
			state := g.State()
			if state.CurrentPlayer != tt.current || state.RoundPhase != game.RoundBidding || state.EliminationLimit != before.EliminationLimit || state.LastBid != nil || state.RoundID != before.RoundID {
				t.Fatalf("withdrawal unexpectedly changed round: %+v", state)
			}
			for seat, player := range state.Players {
				if player.Withdrawn != (player.ID == tt.departing) || player.HandSize != 1 || !slices.Equal(player.Hand, before.Players[seat].Hand) {
					t.Fatalf("incorrect withdrawal/card retention: %+v", player)
				}
			}
			if !slices.Equal(state.Undealt, before.Undealt) {
				t.Error("withdrawal changed undealt cards")
			}
			if err := g.PlaceBid(tt.current, rankClaim(game.HighCard, game.Nine, 0)); err != nil {
				t.Fatal(err)
			}
			if g.State().CurrentPlayer != tt.afterBid {
				t.Error("bidding did not skip the withdrawn seat")
			}
			state = g.State()
			for _, action := range []func() error{
				func() error { return g.PlaceBid(tt.departing, rankClaim(game.Pair, game.Ace, 0)) },
				func() error { return g.Challenge(tt.departing) },
				func() error { return g.Withdraw(tt.departing) },
			} {
				if err := action(); !errors.Is(err, game.ErrInactivePlayer) {
					t.Fatalf("withdrawn actor error = %v; want ErrInactivePlayer", err)
				}
				if !reflect.DeepEqual(state, g.State()) {
					t.Fatal("withdrawn actor changed state")
				}
			}
		})
	}
}

func TestWithdrawnBidRemainsChallengeable(t *testing.T) {
	for _, satisfied := range []bool{true, false} {
		name := "false claim"
		if satisfied {
			name = "true claim"
		}
		t.Run(name, func(t *testing.T) {
			g := newTestGame(t, []game.PlayerID{"a", "b", "c"}, "a")
			before := g.State()
			bid := rankClaim(game.FourOfAKind, game.Ace, 0)
			if satisfied {
				bid = rankClaim(game.HighCard, before.Players[0].Hand[0].Rank, 0)
			}
			if err := g.PlaceBid("a", bid); err != nil {
				t.Fatal(err)
			}
			if err := g.Withdraw("a"); err != nil {
				t.Fatal(err)
			}
			if state := g.State(); state.CurrentPlayer != "b" || state.LastBid.Bid != bid || state.LastBid.PlayerID != "a" {
				t.Fatalf("withdrawal discarded bid or changed another player's turn: %+v", state)
			}
			if err := g.Challenge("b"); err != nil {
				t.Fatal(err)
			}
			resolved := g.State()
			wantLoser, wantSize := game.PlayerID("a"), 1
			if satisfied {
				wantLoser, wantSize = "b", 2
			}
			if resolved.LastResult.Loser != wantLoser || resolved.LastResult.ClaimSatisfied != satisfied || resolved.Players[0].HandSize != 1 || resolved.Players[1].HandSize != wantSize || resolved.NextStarter != "b" {
				t.Fatalf("incorrect departed-bidder outcome: %+v", resolved)
			}
			if len(resolved.LastResult.RevealedHands) != 3 || !slices.Equal(resolved.LastResult.RevealedHands[0].Cards, before.Players[0].Hand) {
				t.Fatal("departed player's dealt hand was not revealed")
			}
			if err := g.NextRound("b", rand.New(rand.NewSource(8))); err != nil {
				t.Fatal(err)
			}
			state := g.State()
			if !state.Players[0].Withdrawn || len(state.Players[0].Hand) != 0 || state.CurrentPlayer != "b" {
				t.Fatalf("departed player was dealt into next round: %+v", state)
			}
			if !reflect.DeepEqual(state.LastResult, resolved.LastResult) {
				t.Error("redealing changed departed player's reveal")
			}
			assertRoundCards(t, state)
		})
	}
}

func TestWithdrawDuringResolution(t *testing.T) {
	tests := []struct {
		name      string
		departing game.PlayerID
		next      game.PlayerID
	}{
		{"next starter leaves", "a", "b"},
		{"other participant leaves", "c", "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := resolvedThreePlayerGame(t)
			before := g.State()
			if err := g.Withdraw(tt.departing); err != nil {
				t.Fatal(err)
			}
			state := g.State()
			if state.NextStarter != tt.next || state.RoundPhase != game.RoundResolution || !reflect.DeepEqual(state.LastResult, before.LastResult) || state.EliminationLimit != before.EliminationLimit {
				t.Fatalf("incorrect pending round after departure: %+v", state)
			}
			if err := g.NextRound(tt.next, rand.New(rand.NewSource(8))); err != nil {
				t.Fatal(err)
			}
			assertRoundCards(t, g.State())
		})
	}
}

func TestNextStarterPassesAcrossDepartures(t *testing.T) {
	g := newTestGame(t, []game.PlayerID{"a", "b", "c", "d"}, "d")
	if err := g.PlaceBid("d", game.Bid{Category: game.RoyalStraightFlush, Suit: game.Clubs}); err != nil {
		t.Fatal(err)
	}
	if err := g.Challenge("a"); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ departing, next game.PlayerID }{{"d", "a"}, {"a", "b"}} {
		if err := g.Withdraw(tt.departing); err != nil {
			t.Fatal(err)
		}
		if g.State().NextStarter != tt.next {
			t.Fatalf("after %q left, starter = %q; want %q", tt.departing, g.State().NextStarter, tt.next)
		}
	}
	if err := g.NextRound("b", rand.New(rand.NewSource(8))); err != nil {
		t.Fatal(err)
	}
	assertRoundCards(t, g.State())
}

func TestWithdrawDefersLimitRecalculation(t *testing.T) {
	g := newTestGame(t, participants(13), "player-0")
	if err := g.Withdraw("player-12"); err != nil {
		t.Fatal(err)
	}
	if g.State().EliminationLimit != 2 {
		t.Fatal("withdrawal changed current round's limit")
	}
	rank := g.State().Players[0].Hand[0].Rank
	if err := g.PlaceBid("player-0", rankClaim(game.HighCard, rank, 0)); err != nil {
		t.Fatal(err)
	}
	if err := g.Challenge("player-1"); err != nil {
		t.Fatal(err)
	}
	state := g.State()
	if !state.Players[1].Eliminated || state.NextStarter != "player-2" || state.LastResult.EliminationLimit != 2 {
		t.Fatalf("withdrawal changed the completed round's penalty: %+v", state)
	}
	if err := g.NextRound(state.NextStarter, rand.New(rand.NewSource(8))); err != nil {
		t.Fatal(err)
	}
	if g.State().EliminationLimit != 3 {
		t.Fatal("next round did not recalculate limit for eleven survivors")
	}
	assertRoundCards(t, g.State())
}

func TestWithdrawEndsGameImmediately(t *testing.T) {
	for _, resolved := range []bool{false, true} {
		name := "during bidding"
		if resolved {
			name = "during resolution"
		}
		t.Run(name, func(t *testing.T) {
			g := newTestGame(t, []game.PlayerID{"a", "b"}, "a")
			if resolved {
				if err := g.PlaceBid("a", rankClaim(game.FourOfAKind, game.Ace, 0)); err != nil {
					t.Fatal(err)
				}
				if err := g.Challenge("b"); err != nil {
					t.Fatal(err)
				}
			}
			before := g.State()
			if err := g.Withdraw("a"); err != nil {
				t.Fatal(err)
			}
			state := g.State()
			if state.Winner != "b" || state.RoundID != before.RoundID || !reflect.DeepEqual(state.LastResult, before.LastResult) {
				t.Fatalf("departure did not immediately preserve game outcome: %+v", state)
			}
			for seat := range state.Players {
				if !slices.Equal(state.Players[seat].Hand, before.Players[seat].Hand) {
					t.Error("terminal departure discarded final-round cards")
				}
			}
			assertFinishedGame(t, g)
		})
	}
}

func TestWithdrawRejectsInvalidActions(t *testing.T) {
	tests := []struct {
		name  string
		actor game.PlayerID
		setup func(*testing.T) *game.Game
		want  error
	}{
		{"uninitialized", "a", func(*testing.T) *game.Game { return &game.Game{} }, game.ErrWrongPhase},
		{"unknown participant", "outsider", resolvedThreePlayerGame, game.ErrUnknownPlayer},
		{"already withdrawn", "a", func(t *testing.T) *game.Game {
			g := resolvedThreePlayerGame(t)
			if err := g.Withdraw("a"); err != nil {
				t.Fatal(err)
			}
			return g
		}, game.ErrInactivePlayer},
		{"eliminated participant", "player-1", func(t *testing.T) *game.Game {
			g := newTestGame(t, participants(13), "player-0")
			if err := g.PlaceBid("player-0", rankClaim(game.HighCard, g.State().Players[0].Hand[0].Rank, 0)); err != nil {
				t.Fatal(err)
			}
			if err := g.Challenge("player-1"); err != nil {
				t.Fatal(err)
			}
			return g
		}, game.ErrInactivePlayer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.setup(t)
			before := g.State()
			if err := g.Withdraw(tt.actor); !errors.Is(err, tt.want) {
				t.Errorf("Withdraw error = %v; want %v", err, tt.want)
			}
			if !reflect.DeepEqual(before, g.State()) {
				t.Error("rejected withdrawal changed state")
			}
		})
	}
}
