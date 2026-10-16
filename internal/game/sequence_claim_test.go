package game_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"falsepoker/internal/game"
)

func TestStraightClaims(t *testing.T) {
	low := []game.Card{
		{Rank: game.Nine, Suit: game.Clubs},
		{Rank: game.Ten, Suit: game.Diamonds},
		{Rank: game.Jack, Suit: game.Hearts},
		{Rank: game.Queen, Suit: game.Spades},
		{Rank: game.King, Suit: game.Clubs},
	}
	high := joinCards(low[1:], []game.Card{{Rank: game.Ace, Suit: game.Hearts}})
	both := joinCards(low, []game.Card{{Rank: game.Ace, Suit: game.Hearts}})
	tests := []struct {
		name     string
		category game.BidCategory
		cards    []game.Card
		want     bool
	}{
		{"low mixed suits", game.LowStraight, low, true},
		{"high mixed suits", game.HighStraight, high, true},
		{"high does not substitute low", game.LowStraight, high, false},
		{"low does not substitute high", game.HighStraight, low, false},
		{"both contains low", game.LowStraight, both, true},
		{"both contains high", game.HighStraight, both, true},
		{"low surplus ranks", game.LowStraight, joinCards(low, rankCards(game.Ace, 4)), true},
		{"high surplus ranks", game.HighStraight, joinCards(high, rankCards(game.Nine, 4)), true},
		{"four of a kind is not low straight", game.LowStraight, rankCards(game.King, 4), false},
		{"four of a kind is not high straight", game.HighStraight, rankCards(game.Ace, 4), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertClaim(t, game.Bid{Category: tt.category}, tt.cards, tt.want)
		})
	}
}

func TestSuitedClaims(t *testing.T) {
	for _, suit := range []game.Suit{game.Clubs, game.Diamonds, game.Hearts, game.Spades} {
		t.Run(fmt.Sprintf("suit %d", suit), func(t *testing.T) {
			otherSuit := game.Clubs
			if suit == otherSuit {
				otherSuit = game.Hearts
			}
			low := suitedCards(suit, game.Nine, game.Ten, game.Jack, game.Queen, game.King)
			high := suitedCards(suit, game.Ten, game.Jack, game.Queen, game.King, game.Ace)
			nonconsecutive := suitedCards(suit, game.Nine, game.Ten, game.Jack, game.Queen, game.Ace)
			allSix := suitedCards(suit, game.Nine, game.Ten, game.Jack, game.Queen, game.King, game.Ace)
			wrongSuit := suitedCards(otherSuit, game.Nine, game.Ten, game.Jack, game.Queen, game.King, game.Ace)
			tests := []struct {
				name     string
				category game.BidCategory
				cards    []game.Card
				want     bool
			}{
				{"flush nonconsecutive five", game.Flush, nonconsecutive, true},
				{"flush four", game.Flush, low[:4], false},
				{"flush four plus other suit", game.Flush, joinCards(low[:4], suitedCards(otherSuit, game.King)), false},
				{"flush six", game.Flush, allSix, true},
				{"flush wrong suit", game.Flush, wrongSuit, false},
				{"straight flush low", game.StraightFlush, low, true},
				{"straight flush high only", game.StraightFlush, high, false},
				{"straight flush nonconsecutive", game.StraightFlush, nonconsecutive, false},
				{"straight flush six", game.StraightFlush, allSix, true},
				{"straight flush wrong suit", game.StraightFlush, wrongSuit, false},
				{"straight flush unrelated surplus", game.StraightFlush, joinCards(low, wrongSuit), true},
				{"royal straight flush high", game.RoyalStraightFlush, high, true},
				{"royal straight flush low only", game.RoyalStraightFlush, low, false},
				{"royal straight flush nonconsecutive", game.RoyalStraightFlush, nonconsecutive, false},
				{"royal straight flush six", game.RoyalStraightFlush, allSix, true},
				{"royal straight flush wrong suit", game.RoyalStraightFlush, wrongSuit, false},
				{"royal straight flush unrelated surplus", game.RoyalStraightFlush, joinCards(high, wrongSuit), true},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					assertClaim(t, game.Bid{Category: tt.category, Suit: suit}, tt.cards, tt.want)
				})
			}
		})
	}
}

func TestStraightMissingEachRank(t *testing.T) {
	tests := []struct {
		name     string
		category game.BidCategory
		ranks    []game.Rank
	}{
		{"low", game.LowStraight, []game.Rank{game.Nine, game.Ten, game.Jack, game.Queen, game.King}},
		{"high", game.HighStraight, []game.Rank{game.Ten, game.Jack, game.Queen, game.King, game.Ace}},
	}
	for _, tt := range tests {
		for missing, rank := range tt.ranks {
			t.Run(fmt.Sprintf("%s missing %d", tt.name, rank), func(t *testing.T) {
				cards := suitedCards(game.Clubs, tt.ranks...)
				cards = slices.Delete(cards, missing, missing+1)
				assertClaim(t, game.Bid{Category: tt.category}, cards, false)
				// Five cards, but still only four distinct ranks.
				cards = append(cards, game.Card{Rank: cards[0].Rank, Suit: game.Hearts})
				assertClaim(t, game.Bid{Category: tt.category}, cards, false)
			})
		}
	}
}

func TestStraightFlushMissingEachRank(t *testing.T) {
	tests := []struct {
		name     string
		category game.BidCategory
		ranks    []game.Rank
	}{
		{"straight flush", game.StraightFlush, []game.Rank{game.Nine, game.Ten, game.Jack, game.Queen, game.King}},
		{"royal straight flush", game.RoyalStraightFlush, []game.Rank{game.Ten, game.Jack, game.Queen, game.King, game.Ace}},
	}
	for _, tt := range tests {
		for _, suit := range []game.Suit{game.Clubs, game.Diamonds, game.Hearts, game.Spades} {
			for missing, rank := range tt.ranks {
				t.Run(fmt.Sprintf("%s suit %d missing %d", tt.name, suit, rank), func(t *testing.T) {
					cards := suitedCards(suit, tt.ranks...)
					cards = slices.Delete(cards, missing, missing+1)
					bid := game.Bid{Category: tt.category, Suit: suit}
					assertClaim(t, bid, cards, false)
					otherSuit := game.Clubs
					if suit == otherSuit {
						otherSuit = game.Hearts
					}
					// The missing rank in another suit cannot complete this claim.
					cards = append(cards, game.Card{Rank: rank, Suit: otherSuit})
					assertClaim(t, bid, cards, false)
					// Adding another matching-suit rank gives five of the named suit,
					// but the sequence is still incomplete.
					extra := game.Ace
					if tt.category == game.RoyalStraightFlush {
						extra = game.Nine
					}
					cards = append(cards, game.Card{Rank: extra, Suit: suit})
					assertClaim(t, bid, cards, false)
					assertClaim(t, game.Bid{Category: game.Flush, Suit: suit}, cards, true)
				})
			}
		}
	}
}

func TestSequenceAndSuitInvalidInputs(t *testing.T) {
	low := suitedCards(game.Hearts, game.Nine, game.Ten, game.Jack, game.Queen, game.King)
	tests := []struct {
		name  string
		bid   game.Bid
		cards []game.Card
		want  error
	}{
		{"straight extra suit", game.Bid{Category: game.LowStraight, Suit: game.Hearts}, low, game.ErrInvalidBid},
		{"flush missing suit", game.Bid{Category: game.Flush}, low, game.ErrInvalidBid},
		{"royal invalid suit", game.Bid{Category: game.RoyalStraightFlush, Suit: 5}, low, game.ErrInvalidBid},
		{"flush extra rank", game.Bid{Category: game.Flush, Suit: game.Hearts, PrimaryRank: game.King}, low, game.ErrInvalidBid},
		{"duplicate after satisfied straight", game.Bid{Category: game.LowStraight}, joinCards(low, low[:1]), game.ErrInvalidCards},
		{"invalid card after satisfied flush", game.Bid{Category: game.Flush, Suit: game.Hearts}, joinCards(low, []game.Card{{}}), game.ErrInvalidCards},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.bid.IsSatisfiedBy(tt.cards)
			if got || !errors.Is(err, tt.want) {
				t.Fatalf("IsSatisfiedBy = (%t, %v); want (false, %v)", got, err, tt.want)
			}
		})
	}
}

func TestAllClaimsFullDeck(t *testing.T) {
	for _, tt := range bidCategories {
		t.Run(tt.name, func(t *testing.T) {
			assertClaim(t, tt.bid, game.NewDeck().Cards(), true)
		})
	}
}

func suitedCards(suit game.Suit, ranks ...game.Rank) []game.Card {
	cards := make([]game.Card, len(ranks))
	for i, rank := range ranks {
		cards[i] = game.Card{Rank: rank, Suit: suit}
	}
	return cards
}

func assertClaim(t *testing.T, bid game.Bid, cards []game.Card, want bool) {
	t.Helper()
	before := slices.Clone(cards)
	got, err := bid.IsSatisfiedBy(cards)
	if err != nil || got != want {
		t.Fatalf("claim %+v on %v = (%t, %v); want (%t, nil)", bid, cards, got, err, want)
	}
	if !slices.Equal(cards, before) {
		t.Fatal("evaluation mutated dealt cards")
	}
	// Card order must not affect a claim over the dealt union.
	reversed := slices.Clone(cards)
	slices.Reverse(reversed)
	got, err = bid.IsSatisfiedBy(reversed)
	if err != nil || got != want {
		t.Fatalf("reversed union = (%t, %v); want (%t, nil)", got, err, want)
	}
}
