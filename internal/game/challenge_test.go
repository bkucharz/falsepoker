package game_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/bkucharz/falsepoker/internal/game"
)

func TestChallengeOutcome(t *testing.T) {
	tests := []struct {
		name      string
		satisfied bool
		loser     game.PlayerID
	}{
		{"true claim penalizes challenger", true, "b"},
		{"false claim penalizes bidder", false, "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newTestGame(t, []game.PlayerID{"a", "b", "c"}, "a")
			before := g.State()
			bid := rankClaim(game.FourOfAKind, game.Ace, 0) // Impossible with three dealt cards.
			if tt.satisfied {
				bid = rankClaim(game.HighCard, before.Players[0].Hand[0].Rank, 0)
			}
			if err := g.PlaceBid("a", bid); err != nil {
				t.Fatal(err)
			}
			if err := g.Challenge("b"); err != nil {
				t.Fatal(err)
			}
			state := g.State()
			if state.Phase != game.GamePlaying || state.RoundPhase != game.RoundResolution || state.CurrentPlayer != "" || state.Winner != "" {
				t.Fatalf("unexpected resolved state: %+v", state)
			}
			result := state.LastResult
			if result == nil || result.RoundID != before.RoundID || result.EliminationLimit != before.EliminationLimit || result.Challenger != "b" || result.Loser != tt.loser || result.ClaimSatisfied != tt.satisfied {
				t.Fatalf("unexpected round result: %+v", result)
			}
			if result.ChallengedBid != (game.PlacedBid{PlayerID: "a", Bid: bid}) || *state.LastBid != result.ChallengedBid {
				t.Error("result did not retain challenged bid and bidder")
			}
			if len(result.RevealedHands) != len(before.Players) {
				t.Fatalf("revealed %d hands; want %d", len(result.RevealedHands), len(before.Players))
			}
			for seat, player := range state.Players {
				wantSize := 1
				if player.ID == tt.loser {
					wantSize++
				}
				if player.HandSize != wantSize || player.Eliminated || player.Withdrawn {
					t.Errorf("incorrect penalty for %+v", player)
				}
				reveal := result.RevealedHands[seat]
				if reveal.PlayerID != player.ID || !slices.Equal(reveal.Cards, before.Players[seat].Hand) || !slices.Equal(player.Hand, before.Players[seat].Hand) {
					t.Error("resolution changed dealt cards or lost their ownership")
				}
			}
			if state.RoundID != before.RoundID || state.EliminationLimit != before.EliminationLimit || !slices.Equal(state.Undealt, before.Undealt) {
				t.Error("resolution advanced a round, changed its limit, or dealt cards")
			}
		})
	}
}

func TestChallengeEvaluatesOnlyLastBid(t *testing.T) {
	g := newTestGame(t, []game.PlayerID{"a", "b", "c"}, "a")
	opening := rankClaim(game.HighCard, g.State().Players[0].Hand[0].Rank, 0)
	if err := g.PlaceBid("a", opening); err != nil {
		t.Fatal(err)
	}
	latest := rankClaim(game.FourOfAKind, game.Ace, 0)
	if err := g.PlaceBid("b", latest); err != nil {
		t.Fatal(err)
	}
	if err := g.Challenge("c"); err != nil {
		t.Fatal(err)
	}
	result := g.State().LastResult
	if result.ClaimSatisfied || result.Loser != "b" || result.ChallengedBid != (game.PlacedBid{PlayerID: "b", Bid: latest}) {
		t.Fatalf("challenge evaluated an earlier bid: %+v", result)
	}
}

func TestChallengeExcludesUndealtCards(t *testing.T) {
	g := newTestGame(t, []game.PlayerID{"a", "b"}, "a")
	state := g.State()
	claimed := game.Nine
	for claimed == state.Players[0].Hand[0].Rank || claimed == state.Players[1].Hand[0].Rank {
		claimed++
	}
	if err := g.PlaceBid("a", rankClaim(game.HighCard, claimed, 0)); err != nil {
		t.Fatal(err)
	}
	if err := g.Challenge("b"); err != nil {
		t.Fatal(err)
	}
	if result := g.State().LastResult; result.ClaimSatisfied || result.Loser != "a" {
		t.Fatalf("undealt cards satisfied a claim: %+v", result)
	}
}

func TestChallengeAllCategoriesAcrossHands(t *testing.T) {
	for _, tt := range bidCategories {
		t.Run(tt.name, func(t *testing.T) {
			g := newTestGame(t, participants(24), "player-0")
			if err := g.PlaceBid("player-0", tt.bid); err != nil {
				t.Fatal(err)
			}
			if err := g.Challenge("player-1"); err != nil {
				t.Fatal(err)
			}
			state := g.State()
			if !state.LastResult.ClaimSatisfied || state.LastResult.Loser != "player-1" || !state.Players[1].Eliminated || state.Players[1].HandSize != 1 {
				t.Fatalf("incorrect challenge outcome: %+v", state)
			}
			var revealed []game.Card
			for _, hand := range state.LastResult.RevealedHands {
				revealed = append(revealed, hand.Cards...)
			}
			assertSameCards(t, game.NewDeck().Cards(), revealed)
		})
	}
}

func TestChallengeUsesCompletedRoundLimit(t *testing.T) {
	g := newTestGame(t, participants(13), "player-0")
	bid := rankClaim(game.HighCard, g.State().Players[0].Hand[0].Rank, 0)
	if err := g.PlaceBid("player-0", bid); err != nil {
		t.Fatal(err)
	}
	if err := g.Challenge("player-1"); err != nil {
		t.Fatal(err)
	}
	state := g.State()
	if !state.Players[1].Eliminated || state.EliminationLimit != 2 || state.LastResult.EliminationLimit != 2 {
		t.Fatalf("resolution used the next round's larger limit: %+v", state)
	}
}

func TestChallengeRejectsInvalidActions(t *testing.T) {
	tests := []struct {
		name    string
		opening bool
		actor   game.PlayerID
		want    error
	}{
		{"no opening bid", false, "a", game.ErrNoBid},
		{"unknown participant", true, "outsider", game.ErrUnknownPlayer},
		{"out of turn", true, "c", game.ErrOutOfTurn},
		{"bidder cannot challenge own bid", true, "a", game.ErrOutOfTurn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newTestGame(t, []game.PlayerID{"a", "b", "c"}, "a")
			if tt.opening {
				if err := g.PlaceBid("a", rankClaim(game.Pair, game.Ace, 0)); err != nil {
					t.Fatal(err)
				}
			}
			before := g.State()
			if err := g.Challenge(tt.actor); !errors.Is(err, tt.want) {
				t.Errorf("Challenge error = %v; want %v", err, tt.want)
			}
			if !reflect.DeepEqual(before, g.State()) {
				t.Error("invalid challenge mutated state")
			}
		})
	}
}

func TestResolvedRoundRejectsFurtherActions(t *testing.T) {
	g := newTestGame(t, []game.PlayerID{"a", "b", "c"}, "a")
	if err := g.PlaceBid("a", rankClaim(game.FourOfAKind, game.Ace, 0)); err != nil {
		t.Fatal(err)
	}
	if err := g.Challenge("b"); err != nil {
		t.Fatal(err)
	}
	before := g.State()
	tests := []struct {
		name string
		act  func() error
	}{
		{"repeat challenge", func() error { return g.Challenge("b") }},
		{"different challenger", func() error { return g.Challenge("c") }},
		{"bid during reveal", func() error { return g.PlaceBid("b", rankClaim(game.Pair, game.Nine, 0)) }},
		{"eligibility during reveal", func() error { return g.ValidateChallenge("b") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.act(); !errors.Is(err, game.ErrWrongPhase) {
				t.Errorf("error = %v; want ErrWrongPhase", err)
			}
			if !reflect.DeepEqual(before, g.State()) {
				t.Error("resolved state or penalty changed")
			}
		})
	}
}

func TestRoundResultOwnership(t *testing.T) {
	g := newTestGame(t, []game.PlayerID{"a", "b", "c"}, "a")
	if err := g.PlaceBid("a", rankClaim(game.Pair, game.Ace, 0)); err != nil {
		t.Fatal(err)
	}
	if err := g.Challenge("b"); err != nil {
		t.Fatal(err)
	}
	before := g.State()
	state := g.State()
	state.LastResult.RoundID = 99
	state.LastResult.ChallengedBid.Bid = game.Bid{}
	state.LastResult.Loser = "changed"
	state.LastResult.RevealedHands[0].PlayerID = "changed"
	state.LastResult.RevealedHands[0].Cards[0] = game.Card{}
	state.LastResult.RevealedHands[0].Cards = append(state.LastResult.RevealedHands[0].Cards, make([]game.Card, 24)...)
	state.LastResult.RevealedHands = append(state.LastResult.RevealedHands, game.RevealedHand{})
	state.Players[1].Hand[0] = game.Card{}
	state.LastBid.Bid = game.Bid{}
	if !reflect.DeepEqual(before, g.State()) {
		t.Fatal("mutating the snapshot changed the retained result or hands")
	}
}
