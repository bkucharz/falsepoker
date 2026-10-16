package game_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/bkucharz/falsepoker/internal/game"
)

// Ordered explicitly by the documented hierarchy, rather than enum values.
var bidCategories = []struct {
	name string
	bid  game.Bid
}{
	{"high card", game.Bid{Category: game.HighCard, PrimaryRank: game.Ace}},
	{"pair", game.Bid{Category: game.Pair, PrimaryRank: game.Nine}},
	{"two pairs", game.Bid{Category: game.TwoPairs, PrimaryRank: game.Ace, SecondaryRank: game.King}},
	{"low straight", game.Bid{Category: game.LowStraight}},
	{"high straight", game.Bid{Category: game.HighStraight}},
	{"three of a kind", game.Bid{Category: game.ThreeOfAKind, PrimaryRank: game.Ace}},
	{"full house", game.Bid{Category: game.FullHouse, PrimaryRank: game.Ace, SecondaryRank: game.King}},
	{"flush", game.Bid{Category: game.Flush, Suit: game.Clubs}},
	{"four of a kind", game.Bid{Category: game.FourOfAKind, PrimaryRank: game.Nine}},
	{"straight flush", game.Bid{Category: game.StraightFlush, Suit: game.Spades}},
	{"royal straight flush", game.Bid{Category: game.RoyalStraightFlush, Suit: game.Clubs}},
}

func TestBidValidateCategories(t *testing.T) {
	for _, tt := range bidCategories {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.bid.Validate(); err != nil {
				t.Fatalf("valid bid %+v rejected: %v", tt.bid, err)
			}
		})
	}
}

func TestBidValidateInvalid(t *testing.T) {
	tests := []struct {
		name string
		bid  game.Bid
	}{
		{"zero bid", game.Bid{}},
		{"unknown category", game.Bid{Category: 255}},
		{"missing primary rank", game.Bid{Category: game.Pair}},
		{"rank below nine", game.Bid{Category: game.Pair, PrimaryRank: 8}},
		{"rank above ace", game.Bid{Category: game.Pair, PrimaryRank: 15}},
		{"extra secondary rank", game.Bid{Category: game.Pair, PrimaryRank: game.King, SecondaryRank: game.Nine}},
		{"extra suit on rank bid", game.Bid{Category: game.Pair, PrimaryRank: game.King, Suit: game.Hearts}},
		{"full house missing primary", game.Bid{Category: game.FullHouse, SecondaryRank: game.King}},
		{"full house missing secondary", game.Bid{Category: game.FullHouse, PrimaryRank: game.King}},
		{"full house invalid primary", game.Bid{Category: game.FullHouse, PrimaryRank: 8, SecondaryRank: game.King}},
		{"full house invalid secondary", game.Bid{Category: game.FullHouse, PrimaryRank: game.King, SecondaryRank: 15}},
		{"full house extra suit", game.Bid{Category: game.FullHouse, PrimaryRank: game.King, SecondaryRank: game.Nine, Suit: game.Clubs}},
		{"full house same ranks", game.Bid{Category: game.FullHouse, PrimaryRank: game.Ace, SecondaryRank: game.Ace}},
		{"two pairs missing primary", game.Bid{Category: game.TwoPairs, SecondaryRank: game.Nine}},
		{"two pairs missing secondary", game.Bid{Category: game.TwoPairs, PrimaryRank: game.King}},
		{"two pairs invalid primary", game.Bid{Category: game.TwoPairs, PrimaryRank: 15, SecondaryRank: game.Nine}},
		{"two pairs invalid secondary", game.Bid{Category: game.TwoPairs, PrimaryRank: game.King, SecondaryRank: 8}},
		{"two pairs extra suit", game.Bid{Category: game.TwoPairs, PrimaryRank: game.King, SecondaryRank: game.Nine, Suit: game.Hearts}},
		{"two pairs same ranks", game.Bid{Category: game.TwoPairs, PrimaryRank: game.King, SecondaryRank: game.King}},
		{"two pairs reversed", game.Bid{Category: game.TwoPairs, PrimaryRank: game.Nine, SecondaryRank: game.King}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.bid.Validate(); !errors.Is(err, game.ErrInvalidBid) {
				t.Fatalf("Validate(%+v) error = %v; want ErrInvalidBid", tt.bid, err)
			}
		})
	}
}

func TestBidValidateParameterShapes(t *testing.T) {
	// Apply each violation to every category, including categories sharing a shape.
	for _, tt := range bidCategories {
		t.Run(tt.name, func(t *testing.T) {
			mutations := []struct {
				name   string
				mutate func(*game.Bid)
			}{
				{"primary rank", func(b *game.Bid) {
					if b.PrimaryRank == 0 {
						b.PrimaryRank = game.Nine
					} else {
						b.PrimaryRank = 0
					}
				}},
				{"secondary rank", func(b *game.Bid) {
					if b.SecondaryRank == 0 {
						b.SecondaryRank = game.Nine
					} else {
						b.SecondaryRank = 0
					}
				}},
				{"suit", func(b *game.Bid) {
					if b.Suit == 0 {
						b.Suit = game.Hearts
					} else {
						b.Suit = 0
					}
				}},
			}
			for _, mutation := range mutations {
				t.Run(mutation.name, func(t *testing.T) {
					bid := tt.bid
					mutation.mutate(&bid)
					if err := bid.Validate(); !errors.Is(err, game.ErrInvalidBid) {
						t.Fatalf("Validate(%+v) error = %v; want ErrInvalidBid", bid, err)
					}
				})
			}
		})
	}
}

func TestBidValidateRankBoundaries(t *testing.T) {
	for _, category := range []game.BidCategory{game.HighCard, game.Pair, game.ThreeOfAKind, game.FourOfAKind} {
		for _, rank := range []game.Rank{0, 8, game.Nine, game.Ten, game.Jack, game.Queen, game.King, game.Ace, 15, 255} {
			t.Run(fmt.Sprintf("category %d rank %d", category, rank), func(t *testing.T) {
				err := (game.Bid{Category: category, PrimaryRank: rank}).Validate()
				wantValid := rank >= game.Nine && rank <= game.Ace
				if wantValid && err != nil {
					t.Fatalf("valid rank rejected: %v", err)
				}
				if !wantValid && !errors.Is(err, game.ErrInvalidBid) {
					t.Fatalf("invalid rank error = %v; want ErrInvalidBid", err)
				}
			})
		}
	}
}

func TestBidValidateSuitBoundaries(t *testing.T) {
	for _, category := range []game.BidCategory{game.Flush, game.StraightFlush, game.RoyalStraightFlush} {
		for _, suit := range []game.Suit{0, game.Clubs, game.Diamonds, game.Hearts, game.Spades, 5, 255} {
			t.Run(fmt.Sprintf("category %d suit %d", category, suit), func(t *testing.T) {
				err := (game.Bid{Category: category, Suit: suit}).Validate()
				wantValid := suit >= game.Clubs && suit <= game.Spades
				if wantValid && err != nil {
					t.Fatalf("valid suit rejected: %v", err)
				}
				if !wantValid && !errors.Is(err, game.ErrInvalidBid) {
					t.Fatalf("invalid suit error = %v; want ErrInvalidBid", err)
				}
			})
		}
	}
}

func TestBidCategoryOrdering(t *testing.T) {
	for i, next := range bidCategories {
		for j, previous := range bidCategories {
			t.Run(next.name+" vs "+previous.name, func(t *testing.T) {
				assertRaise(t, next.bid, previous.bid, i > j)
			})
		}
	}
}

func TestBidRankOrdering(t *testing.T) {
	tests := []struct {
		name     string
		next     game.Bid
		previous game.Bid
	}{
		{"high card", game.Bid{Category: game.HighCard, PrimaryRank: game.Ten}, game.Bid{Category: game.HighCard, PrimaryRank: game.Nine}},
		{"pair", game.Bid{Category: game.Pair, PrimaryRank: game.Ace}, game.Bid{Category: game.Pair, PrimaryRank: game.King}},
		{"three of a kind", game.Bid{Category: game.ThreeOfAKind, PrimaryRank: game.Queen}, game.Bid{Category: game.ThreeOfAKind, PrimaryRank: game.Jack}},
		{"four of a kind", game.Bid{Category: game.FourOfAKind, PrimaryRank: game.King}, game.Bid{Category: game.FourOfAKind, PrimaryRank: game.Queen}},
		{"full house primary dominates", game.Bid{Category: game.FullHouse, PrimaryRank: game.King, SecondaryRank: game.Ten}, game.Bid{Category: game.FullHouse, PrimaryRank: game.Queen, SecondaryRank: game.Ace}},
		{"full house secondary", game.Bid{Category: game.FullHouse, PrimaryRank: game.Nine, SecondaryRank: game.Ace}, game.Bid{Category: game.FullHouse, PrimaryRank: game.Nine, SecondaryRank: game.King}},
		{"two pairs primary dominates", game.Bid{Category: game.TwoPairs, PrimaryRank: game.Ace, SecondaryRank: game.Nine}, game.Bid{Category: game.TwoPairs, PrimaryRank: game.King, SecondaryRank: game.Queen}},
		{"two pairs secondary", game.Bid{Category: game.TwoPairs, PrimaryRank: game.Ace, SecondaryRank: game.King}, game.Bid{Category: game.TwoPairs, PrimaryRank: game.Ace, SecondaryRank: game.Queen}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRaise(t, tt.next, tt.previous, true)
			assertRaise(t, tt.previous, tt.next, false)
			assertRaise(t, tt.next, tt.next, false)
		})
	}
}

func TestBidSuitChangesAreNotRaises(t *testing.T) {
	for _, category := range []game.BidCategory{game.Flush, game.StraightFlush, game.RoyalStraightFlush} {
		for _, first := range []game.Suit{game.Clubs, game.Diamonds, game.Hearts, game.Spades} {
			for _, second := range []game.Suit{game.Clubs, game.Diamonds, game.Hearts, game.Spades} {
				t.Run(fmt.Sprintf("category %d suits %d/%d", category, first, second), func(t *testing.T) {
					assertRaise(t, game.Bid{Category: category, Suit: first}, game.Bid{Category: category, Suit: second}, false)
				})
			}
		}
	}
}

func TestRoyalStraightFlushHasNoHigherBid(t *testing.T) {
	for _, suit := range []game.Suit{game.Clubs, game.Diamonds, game.Hearts, game.Spades} {
		for _, candidate := range bidCategories {
			t.Run(fmt.Sprintf("%s vs royal suit %d", candidate.name, suit), func(t *testing.T) {
				assertRaise(t, candidate.bid, game.Bid{Category: game.RoyalStraightFlush, Suit: suit}, false)
			})
		}
	}
}

func TestBidComparisonRejectsInvalidBids(t *testing.T) {
	valid := game.Bid{Category: game.Pair, PrimaryRank: game.Ace}
	tests := []struct {
		name     string
		next     game.Bid
		previous game.Bid
	}{
		{"invalid new", game.Bid{}, valid},
		{"invalid previous", valid, game.Bid{}},
		{"both invalid", game.Bid{}, game.Bid{}},
		{"higher category with invalid parameters", game.Bid{Category: game.RoyalStraightFlush}, valid},
		{"previous with extra parameters", valid, game.Bid{Category: game.HighCard, PrimaryRank: game.Nine, Suit: game.Clubs}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			higher, err := tt.next.IsHigherThan(tt.previous)
			if !errors.Is(err, game.ErrInvalidBid) || higher {
				t.Fatalf("IsHigherThan = (%t, %v); want (false, ErrInvalidBid)", higher, err)
			}
		})
	}
}

func assertRaise(t *testing.T, next, previous game.Bid, want bool) {
	t.Helper()
	got, err := next.IsHigherThan(previous)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("%+v higher than %+v = %t; want %t", next, previous, got, want)
	}
}
