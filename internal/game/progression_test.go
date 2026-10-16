package game_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"falsepoker/internal/game"
)

func TestNextRoundRedealsSurvivors(t *testing.T) {
	first := resolvedThreePlayerGame(t)
	second := resolvedThreePlayerGame(t)
	before := first.State()
	if before.NextStarter != "a" {
		t.Fatalf("next starter = %q; want a", before.NextStarter)
	}
	for _, g := range []*game.Game{first, second} {
		if err := g.NextRound("a", rand.New(rand.NewSource(7))); err != nil {
			t.Fatal(err)
		}
	}
	state := first.State()
	if !reflect.DeepEqual(state, second.State()) {
		t.Error("identical randomness/configuration produced different next rounds")
	}
	different := resolvedThreePlayerGame(t)
	if err := different.NextRound("a", rand.New(rand.NewSource(8))); err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(state, different.State()) {
		t.Error("different fixed sources produced identical next-round deals")
	}
	if state.RoundID != 2 || state.RoundPhase != game.RoundBidding || state.Phase != game.GamePlaying || state.Starter != "a" || state.CurrentPlayer != "a" || state.NextStarter != "" || state.LastBid != nil {
		t.Fatalf("unexpected new round state: %+v", state)
	}
	if !reflect.DeepEqual(state.LastResult, before.LastResult) {
		t.Fatal("redealing modified or discarded the completed round's result")
	}
	for i, player := range state.Players {
		if player.HandSize != before.Players[i].HandSize || len(player.Hand) != player.HandSize {
			t.Errorf("incorrect hand size after redeal: %+v", player)
		}
	}
	assertRoundCards(t, state)
	// Mutating a new-round snapshot cannot change the retained reveal.
	state.Players[0].Hand[0] = game.Card{}
	if !reflect.DeepEqual(first.State().LastResult, before.LastResult) {
		t.Error("new hands alias the previous round result")
	}
}

func TestNextRoundAfterElimination(t *testing.T) {
	tests := []struct {
		name       string
		count      int
		starter    int
		eliminated int
		next       int
		oldLimit   int
		newLimit   int
	}{
		{"limit increases at twelve survivors", 13, 0, 1, 2, 2, 3},
		{"starter wraps after last seat eliminated", 13, 11, 12, 0, 2, 3},
		{"twenty-four-player boundary", 24, 0, 1, 2, 2, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seats := participants(tt.count)
			g := newTestGame(t, seats, seats[tt.starter])
			rank := g.State().Players[tt.starter].Hand[0].Rank
			if err := g.PlaceBid(seats[tt.starter], rankClaim(game.HighCard, rank, 0)); err != nil {
				t.Fatal(err)
			}
			if err := g.Challenge(seats[tt.eliminated]); err != nil {
				t.Fatal(err)
			}
			resolved := g.State()
			if !resolved.Players[tt.eliminated].Eliminated || resolved.NextStarter != seats[tt.next] || resolved.EliminationLimit != tt.oldLimit {
				t.Fatalf("incorrect elimination/starter selection: %+v", resolved)
			}
			if err := g.NextRound(resolved.NextStarter, rand.New(rand.NewSource(5))); err != nil {
				t.Fatal(err)
			}
			state := g.State()
			if state.EliminationLimit != tt.newLimit || state.LastResult.EliminationLimit != tt.oldLimit || len(state.Players[tt.eliminated].Hand) != 0 || state.CurrentPlayer != seats[tt.next] {
				t.Fatalf("incorrect next round after elimination: %+v", state)
			}
			assertRoundCards(t, state)
		})
	}
}

func TestNextRoundRejectsInvalidActions(t *testing.T) {
	tests := []struct {
		name   string
		actor  game.PlayerID
		setup  func(*testing.T) *game.Game
		nilRNG bool
		want   error
	}{
		{"uninitialized", "a", func(*testing.T) *game.Game { return &game.Game{} }, false, game.ErrWrongPhase},
		{"bidding", "a", func(t *testing.T) *game.Game { return newTestGame(t, []game.PlayerID{"a", "b", "c"}, "a") }, false, game.ErrWrongPhase},
		{"unknown actor", "outsider", resolvedThreePlayerGame, false, game.ErrUnknownPlayer},
		{"wrong starter", "b", resolvedThreePlayerGame, false, game.ErrOutOfTurn},
		{"nil randomness", "a", resolvedThreePlayerGame, true, game.ErrInvalidRandomSource},
		{"withdrawn starter", "a", func(t *testing.T) *game.Game {
			g := resolvedThreePlayerGame(t)
			if err := g.Withdraw("a"); err != nil {
				t.Fatal(err)
			}
			return g
		}, false, game.ErrInactivePlayer},
		{"finished game", "a", func(t *testing.T) *game.Game {
			g := newTestGame(t, []game.PlayerID{"a", "b"}, "a")
			if err := g.Withdraw("b"); err != nil {
				t.Fatal(err)
			}
			return g
		}, false, game.ErrWrongPhase},
		{"already advanced", "a", func(t *testing.T) *game.Game {
			g := resolvedThreePlayerGame(t)
			if err := g.NextRound("a", rand.New(rand.NewSource(9))); err != nil {
				t.Fatal(err)
			}
			return g
		}, false, game.ErrWrongPhase},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.setup(t)
			before := g.State()
			rng, control := rand.New(rand.NewSource(9)), rand.New(rand.NewSource(9))
			if tt.nilRNG {
				rng = nil
			}
			if err := g.NextRound(tt.actor, rng); !errors.Is(err, tt.want) {
				t.Errorf("NextRound error = %v; want %v", err, tt.want)
			}
			if !reflect.DeepEqual(before, g.State()) {
				t.Error("rejected advancement changed state")
			}
			if rng != nil && rng.Int63() != control.Int63() {
				t.Error("rejected actor/phase consumed randomness")
			}
		})
	}
}

func TestCompleteGames(t *testing.T) {
	for _, count := range []int{2, 4, 5, 6, 7, 8, 9, 12, 13, 24} {
		t.Run(fmt.Sprintf("%d participants", count), func(t *testing.T) {
			seats := participants(count)
			g := newTestGame(t, seats, seats[count-1])
			rng := rand.New(rand.NewSource(10))
			for round := 1; round <= count*5; round++ {
				before := g.State()
				assertRoundCards(t, before)
				if before.RoundID != uint64(round) || before.CurrentPlayer != before.Starter || before.LastBid != nil {
					t.Fatalf("incorrect round initialization: %+v", before)
				}
				// A true high-card claim is always available. The next player loses
				// the challenge, exercising penalties and eventual eliminations.
				rank := game.Nine
				for _, player := range before.Players {
					if player.ID == before.CurrentPlayer {
						rank = player.Hand[0].Rank
					}
				}
				if err := g.PlaceBid(before.CurrentPlayer, rankClaim(game.HighCard, rank, 0)); err != nil {
					t.Fatal(err)
				}
				challenger := g.State().CurrentPlayer
				if err := g.Challenge(challenger); err != nil {
					t.Fatal(err)
				}
				resolved := g.State()
				if resolved.LastResult.Loser != challenger || !resolved.LastResult.ClaimSatisfied {
					t.Fatal("scripted true claim produced wrong loser")
				}
				for seat, player := range resolved.Players {
					old := before.Players[seat]
					if player.ID != seats[seat] {
						t.Fatal("seat identity/order changed")
					}
					if player.ID == challenger {
						if old.HandSize+1 == before.EliminationLimit {
							if !player.Eliminated || player.HandSize != old.HandSize {
								t.Fatal("incorrect threshold elimination")
							}
						} else if player.HandSize != old.HandSize+1 || player.Eliminated {
							t.Fatal("incorrect surviving loser penalty")
						}
					} else if !reflect.DeepEqual(player, old) {
						t.Fatal("non-losing participant changed")
					}
				}
				if resolved.Phase == game.GameFinished {
					assertFinishedGame(t, g)
					return
				}
				if err := g.NextRound(resolved.NextStarter, rng); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(g.State().LastResult, resolved.LastResult) {
					t.Fatal("redeal changed previous reveal")
				}
			}
			t.Fatal("game did not reach a winner within the maximum losses per participant")
		})
	}
}

func resolvedThreePlayerGame(t *testing.T) *game.Game {
	t.Helper()
	g := newTestGame(t, []game.PlayerID{"a", "b", "c"}, "a")
	if err := g.PlaceBid("a", rankClaim(game.FourOfAKind, game.Ace, 0)); err != nil {
		t.Fatal(err)
	}
	if err := g.Challenge("b"); err != nil {
		t.Fatal(err)
	}
	return g
}

func assertRoundCards(t *testing.T, state game.GameState) {
	t.Helper()
	var cards []game.Card
	active := 0
	for _, player := range state.Players {
		if player.Eliminated || player.Withdrawn {
			if len(player.Hand) != 0 {
				t.Fatalf("inactive player received cards: %+v", player)
			}
			continue
		}
		active++
		if len(player.Hand) != player.HandSize || player.HandSize < 1 || player.HandSize >= state.EliminationLimit {
			t.Fatalf("invalid current hand size: %+v", player)
		}
		cards = append(cards, player.Hand...)
	}
	if state.EliminationLimit != min(6, 24/active+1) {
		t.Fatalf("incorrect limit for %d survivors: %d", active, state.EliminationLimit)
	}
	cards = append(cards, state.Undealt...)
	assertSameCards(t, game.NewDeck().Cards(), cards)
}

func assertFinishedGame(t *testing.T, g *game.Game) {
	t.Helper()
	state := g.State()
	active := 0
	for _, player := range state.Players {
		if !player.Eliminated && !player.Withdrawn {
			active++
			if player.ID != state.Winner {
				t.Fatal("winner is not the remaining participant")
			}
		}
	}
	if state.Phase != game.GameFinished || active != 1 || state.Winner == "" || state.CurrentPlayer != "" || state.NextStarter != "" {
		t.Fatalf("incorrect terminal state: %+v", state)
	}
	for _, action := range []func() error{
		func() error { return g.PlaceBid(state.Winner, rankClaim(game.HighCard, game.Ace, 0)) },
		func() error { return g.Challenge(state.Winner) },
		func() error { return g.NextRound(state.Winner, rand.New(rand.NewSource(1))) },
		func() error { return g.Withdraw(state.Winner) },
	} {
		if err := action(); !errors.Is(err, game.ErrWrongPhase) {
			t.Fatalf("finished action error = %v; want ErrWrongPhase", err)
		}
		if !reflect.DeepEqual(state, g.State()) {
			t.Fatal("finished game changed")
		}
	}
}
