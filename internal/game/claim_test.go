package game_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/bkucharz/falsepoker/internal/game"
)

func TestRankClaims(t *testing.T) {
	tests := []struct {
		name  string
		bid   game.Bid
		cards []game.Card
		want  bool
	}{
		{"high card present", rankClaim(game.HighCard, game.King, 0), rankCards(game.King, 1), true},
		{"high card missing", rankClaim(game.HighCard, game.King, 0), rankCards(game.Ace, 1), false},
		{"high card surplus", rankClaim(game.HighCard, game.Nine, 0), rankCards(game.Nine, 4), true},
		{"pair present", rankClaim(game.Pair, game.King, 0), rankCards(game.King, 2), true},
		{"pair short", rankClaim(game.Pair, game.King, 0), rankCards(game.King, 1), false},
		{"pair wrong rank", rankClaim(game.Pair, game.King, 0), rankCards(game.Ace, 4), false},
		{"pair in triple", rankClaim(game.Pair, game.King, 0), rankCards(game.King, 3), true},
		{"pair in four", rankClaim(game.Pair, game.King, 0), rankCards(game.King, 4), true},
		{"two pairs present", rankClaim(game.TwoPairs, game.Ace, game.Nine), joinCards(rankCards(game.Ace, 2), rankCards(game.Nine, 2)), true},
		{"two pairs primary short", rankClaim(game.TwoPairs, game.Ace, game.Nine), joinCards(rankCards(game.Ace, 1), rankCards(game.Nine, 2)), false},
		{"two pairs secondary short", rankClaim(game.TwoPairs, game.Ace, game.Nine), joinCards(rankCards(game.Ace, 2), rankCards(game.Nine, 1)), false},
		{"two pairs surplus", rankClaim(game.TwoPairs, game.Ace, game.Nine), joinCards(rankCards(game.Ace, 4), rankCards(game.Nine, 3)), true},
		{"four cannot substitute two pairs", rankClaim(game.TwoPairs, game.Ace, game.Nine), rankCards(game.Ace, 4), false},
		{"unclaimed pair cannot substitute", rankClaim(game.TwoPairs, game.Ace, game.Nine), joinCards(rankCards(game.Ace, 2), rankCards(game.King, 2)), false},
		{"three present", rankClaim(game.ThreeOfAKind, game.Queen, 0), rankCards(game.Queen, 3), true},
		{"three short", rankClaim(game.ThreeOfAKind, game.Queen, 0), rankCards(game.Queen, 2), false},
		{"three wrong rank", rankClaim(game.ThreeOfAKind, game.Queen, 0), rankCards(game.Ace, 4), false},
		{"three in four", rankClaim(game.ThreeOfAKind, game.Queen, 0), rankCards(game.Queen, 4), true},
		{"full house present", rankClaim(game.FullHouse, game.King, game.Ten), joinCards(rankCards(game.King, 3), rankCards(game.Ten, 2)), true},
		{"full house triple short", rankClaim(game.FullHouse, game.King, game.Ten), joinCards(rankCards(game.King, 2), rankCards(game.Ten, 2)), false},
		{"full house pair short", rankClaim(game.FullHouse, game.King, game.Ten), joinCards(rankCards(game.King, 3), rankCards(game.Ten, 1)), false},
		{"full house surplus", rankClaim(game.FullHouse, game.King, game.Ten), joinCards(rankCards(game.King, 4), rankCards(game.Ten, 3)), true},
		{"full house swapped roles", rankClaim(game.FullHouse, game.King, game.Ten), joinCards(rankCards(game.King, 2), rankCards(game.Ten, 3)), false},
		{"full house wrong pair", rankClaim(game.FullHouse, game.King, game.Ten), joinCards(rankCards(game.King, 3), rankCards(game.Ace, 2)), false},
		{"four cannot substitute full house", rankClaim(game.FullHouse, game.King, game.Ten), rankCards(game.King, 4), false},
		{"four present", rankClaim(game.FourOfAKind, game.Jack, 0), rankCards(game.Jack, 4), true},
		{"four short", rankClaim(game.FourOfAKind, game.Jack, 0), rankCards(game.Jack, 3), false},
		{"four wrong rank", rankClaim(game.FourOfAKind, game.Jack, 0), rankCards(game.Ace, 4), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := slices.Clone(tt.cards)
			got, err := tt.bid.IsSatisfiedBy(tt.cards)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("IsSatisfiedBy = %t; want %t", got, tt.want)
			}
			if !slices.Equal(before, tt.cards) {
				t.Error("evaluation mutated dealt cards")
			}
		})
	}
}

func TestRankClaimsEmptyUnion(t *testing.T) {
	tests := []struct {
		name string
		bid  game.Bid
	}{
		{"high card", rankClaim(game.HighCard, game.Ace, 0)},
		{"pair", rankClaim(game.Pair, game.Ace, 0)},
		{"two pairs", rankClaim(game.TwoPairs, game.Ace, game.King)},
		{"three", rankClaim(game.ThreeOfAKind, game.Ace, 0)},
		{"full house", rankClaim(game.FullHouse, game.Ace, game.King)},
		{"four", rankClaim(game.FourOfAKind, game.Ace, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, cards := range [][]game.Card{nil, {}} {
				got, err := tt.bid.IsSatisfiedBy(cards)
				if err != nil || got {
					t.Fatalf("empty union = (%t, %v); want (false, nil)", got, err)
				}
			}
		})
	}
}

func TestRankClaimsAcrossHands(t *testing.T) {
	// Neither hand contains a pair of kings or the claimed full house by itself.
	hands := [][]game.Card{
		{{Rank: game.King, Suit: game.Clubs}, {Rank: game.Ten, Suit: game.Clubs}},
		{{Rank: game.King, Suit: game.Hearts}, {Rank: game.Ten, Suit: game.Hearts}},
		{{Rank: game.King, Suit: game.Spades}},
	}
	tests := []struct {
		name string
		bid  game.Bid
	}{
		{"pair", rankClaim(game.Pair, game.King, 0)},
		{"full house", rankClaim(game.FullHouse, game.King, game.Ten)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, hand := range hands {
				got, err := tt.bid.IsSatisfiedBy(hand)
				if err != nil || got {
					t.Fatalf("individual hand = (%t, %v); want (false, nil)", got, err)
				}
			}
			got, err := tt.bid.IsSatisfiedBy(joinCards(hands...))
			if err != nil || !got {
				t.Fatalf("union = (%t, %v); want (true, nil)", got, err)
			}
		})
	}
}

func TestClaimInvalidInputs(t *testing.T) {
	pair := rankClaim(game.Pair, game.King, 0)
	tests := []struct {
		name  string
		bid   game.Bid
		cards []game.Card
		want  error
	}{
		{"zero bid", game.Bid{}, nil, game.ErrInvalidBid},
		{"unknown category", game.Bid{Category: 255}, nil, game.ErrInvalidBid},
		{"missing rank", rankClaim(game.Pair, 0, 0), rankCards(game.King, 2), game.ErrInvalidBid},
		{"same full house ranks", rankClaim(game.FullHouse, game.King, game.King), rankCards(game.King, 4), game.ErrInvalidBid},
		{"same two pair ranks", rankClaim(game.TwoPairs, game.King, game.King), rankCards(game.King, 4), game.ErrInvalidBid},
		{"reversed two pairs", rankClaim(game.TwoPairs, game.Nine, game.King), nil, game.ErrInvalidBid},
		{"invalid rank low", pair, []game.Card{{Rank: 8, Suit: game.Clubs}}, game.ErrInvalidCards},
		{"invalid rank high", pair, []game.Card{{Rank: 255, Suit: game.Clubs}}, game.ErrInvalidCards},
		{"missing suit", pair, []game.Card{{Rank: game.King}}, game.ErrInvalidCards},
		{"invalid suit", pair, []game.Card{{Rank: game.King, Suit: 255}}, game.ErrInvalidCards},
		{"duplicate cannot make pair", pair, []game.Card{{Rank: game.King, Suit: game.Clubs}, {Rank: game.King, Suit: game.Clubs}}, game.ErrInvalidCards},
		{"invalid card after satisfied claim", pair, joinCards(rankCards(game.King, 2), []game.Card{{}}), game.ErrInvalidCards},
		{"duplicate unrelated card", pair, joinCards(rankCards(game.King, 2), rankCards(game.Ace, 1), rankCards(game.Ace, 1)), game.ErrInvalidCards},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := slices.Clone(tt.cards)
			got, err := tt.bid.IsSatisfiedBy(tt.cards)
			if got || !errors.Is(err, tt.want) {
				t.Fatalf("IsSatisfiedBy = (%t, %v); want (false, %v)", got, err, tt.want)
			}
			if !slices.Equal(before, tt.cards) {
				t.Error("failed evaluation mutated dealt cards")
			}
		})
	}
}

func TestSequenceAndSuitClaimsEmptyUnion(t *testing.T) {
	tests := []struct {
		name string
		bid  game.Bid
	}{
		{"low straight", game.Bid{Category: game.LowStraight}},
		{"high straight", game.Bid{Category: game.HighStraight}},
		{"flush", game.Bid{Category: game.Flush, Suit: game.Clubs}},
		{"straight flush", game.Bid{Category: game.StraightFlush, Suit: game.Clubs}},
		{"royal straight flush", game.Bid{Category: game.RoyalStraightFlush, Suit: game.Clubs}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, cards := range [][]game.Card{nil, {}} {
				got, err := tt.bid.IsSatisfiedBy(cards)
				if got || err != nil {
					t.Fatalf("empty union = (%t, %v); want (false, nil)", got, err)
				}
			}
		})
	}
}

func rankClaim(category game.BidCategory, primary, secondary game.Rank) game.Bid {
	return game.Bid{Category: category, PrimaryRank: primary, SecondaryRank: secondary}
}

// rankCards constructs distinct physical cards, rather than duplicating a card
// to simulate rank multiplicity. Tests request at most four cards of each rank.
func rankCards(rank game.Rank, count int) []game.Card {
	suits := []game.Suit{game.Clubs, game.Diamonds, game.Hearts, game.Spades}
	cards := make([]game.Card, count)
	for i := range cards {
		cards[i] = game.Card{Rank: rank, Suit: suits[i]}
	}
	return cards
}

func joinCards(hands ...[]game.Card) []game.Card {
	var cards []game.Card
	for _, hand := range hands {
		cards = append(cards, hand...)
	}
	return cards
}
