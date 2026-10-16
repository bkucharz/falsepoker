package game

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

func TestChallengePenaltyBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		sizes      []int
		satisfied  bool
		loserSeat  int
		eliminated bool
	}{
		{"challenger survives below limit", []int{1, 4}, true, 1, false},
		{"bidder survives below limit", []int{4, 1}, false, 0, false},
		{"challenger eliminated at limit", []int{1, 5}, true, 1, true},
		{"bidder eliminated at limit", []int{5, 1}, false, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := challengeFixture(t, tt.sizes)
			before := g.State()
			bid := Bid{Category: FourOfAKind, PrimaryRank: Ace}
			if tt.satisfied {
				bid = Bid{Category: HighCard, PrimaryRank: Nine}
			}
			if err := g.PlaceBid("a", bid); err != nil {
				t.Fatal(err)
			}
			if err := g.Challenge("b"); err != nil {
				t.Fatal(err)
			}
			state := g.State()
			loser := state.Players[tt.loserSeat]
			wantSize := tt.sizes[tt.loserSeat]
			if !tt.eliminated {
				wantSize++
			}
			if loser.Eliminated != tt.eliminated || loser.HandSize != wantSize || state.LastResult.Loser != loser.ID {
				t.Fatalf("incorrect boundary penalty: %+v", state)
			}
			if state.LastResult.ClaimSatisfied != tt.satisfied || state.RoundPhase != RoundResolution || state.EliminationLimit != 6 {
				t.Fatalf("incorrect resolution: %+v", state)
			}
			other := state.Players[1-tt.loserSeat]
			if other.HandSize != tt.sizes[1-tt.loserSeat] || other.Eliminated {
				t.Error("non-losing participant was penalized")
			}
			for seat := range state.Players {
				if !reflect.DeepEqual(state.Players[seat].Hand, before.Players[seat].Hand) {
					t.Error("penalty changed the resolved round's cards")
				}
			}
			if tt.eliminated {
				if state.Phase != GameFinished || state.Winner != other.ID {
					t.Fatalf("terminal elimination did not finish game: %+v", state)
				}
			} else if state.Phase != GamePlaying || state.Winner != "" {
				t.Fatalf("nonterminal penalty finished game: %+v", state)
			}
			if err := g.Challenge("b"); !errors.Is(err, ErrWrongPhase) {
				t.Errorf("repeat challenge error = %v; want ErrWrongPhase", err)
			}
			if err := g.PlaceBid(other.ID, Bid{Category: Pair, PrimaryRank: Nine}); !errors.Is(err, ErrWrongPhase) {
				t.Errorf("bid after resolution error = %v; want ErrWrongPhase", err)
			}
			if !reflect.DeepEqual(state, g.State()) {
				t.Error("post-resolution actions changed the result or penalty")
			}
		})
	}
}

func TestChallengeWithdrawnBidder(t *testing.T) {
	for _, satisfied := range []bool{true, false} {
		name := "false bid receives no additional penalty"
		if satisfied {
			name = "withdrawn cards satisfy bid"
		}
		t.Run(name, func(t *testing.T) {
			g := challengeFixture(t, []int{1, 1, 1})
			bid := Bid{Category: FourOfAKind, PrimaryRank: Ace}
			if satisfied {
				// Only a holds a nine in this fixture.
				bid = Bid{Category: HighCard, PrimaryRank: Nine}
			}
			if err := g.PlaceBid("a", bid); err != nil {
				t.Fatal(err)
			}
			g.players[0].Withdrawn = true
			if err := g.Challenge("b"); err != nil {
				t.Fatal(err)
			}
			state := g.State()
			wantLoser, wantChallengerSize := PlayerID("a"), 1
			if satisfied {
				wantLoser, wantChallengerSize = "b", 2
			}
			if state.LastResult.ClaimSatisfied != satisfied || state.LastResult.Loser != wantLoser {
				t.Fatalf("unexpected departed-bidder result: %+v", state.LastResult)
			}
			if !state.Players[0].Withdrawn || state.Players[0].HandSize != 1 || state.Players[0].Eliminated || state.Players[1].HandSize != wantChallengerSize || state.Players[2].HandSize != 1 {
				t.Fatalf("incorrect penalty or restored withdrawn bidder: %+v", state.Players)
			}
			if len(state.LastResult.RevealedHands) != 3 || state.LastResult.RevealedHands[0].Cards[0].Rank != Nine {
				t.Error("withdrawn bidder's hand was omitted from reveal")
			}
			if state.Phase != GamePlaying {
				t.Error("two remaining participants should continue")
			}
		})
	}
}

func TestChallengeEvaluationErrorsAreAtomic(t *testing.T) {
	tests := []struct {
		name    string
		corrupt func(*Game)
		want    error
	}{
		{"invalid dealt card", func(g *Game) { g.players[0].Hand[0] = Card{} }, ErrInvalidCards},
		{"duplicate dealt card", func(g *Game) { g.players[1].Hand[0] = g.players[0].Hand[0] }, ErrInvalidCards},
		{"invalid preceding bid", func(g *Game) { g.lastBid.Bid = Bid{} }, ErrInvalidBid},
		{"unknown preceding bidder", func(g *Game) { g.lastBid.PlayerID = "outsider" }, ErrUnknownPlayer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := challengeFixture(t, []int{1, 1, 1})
			if err := g.PlaceBid("a", Bid{Category: FourOfAKind, PrimaryRank: Ace}); err != nil {
				t.Fatal(err)
			}
			tt.corrupt(g)
			before := g.State()
			if err := g.Challenge("b"); !errors.Is(err, tt.want) {
				t.Errorf("error = %v; want %v", err, tt.want)
			}
			if !reflect.DeepEqual(before, g.State()) {
				t.Error("failed evaluation changed state")
			}
		})
	}
}

// Build later-round hands without implementing progression or exposing setters.
// The full deck is partitioned between real, unique hands and the undealt deck.
func challengeFixture(t *testing.T, sizes []int) *Game {
	t.Helper()
	ids := []PlayerID{"a", "b", "c"}[:len(sizes)]
	g, err := NewGame(ids, "a", rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatal(err)
	}
	g.deck = NewDeck()
	hands, err := g.deck.Deal(sizes)
	if err != nil {
		t.Fatal(err)
	}
	for seat, size := range sizes {
		g.players[seat].HandSize = size
		g.players[seat].Hand = hands[seat]
	}
	g.roundID = 7
	return g
}
