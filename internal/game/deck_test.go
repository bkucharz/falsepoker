package game_test

import (
	"errors"
	"math/rand"
	"slices"
	"testing"

	"github.com/bkucharz/falsepoker/internal/game"
)

func TestNewDeck(t *testing.T) {
	deck := game.NewDeck()
	if deck.Len() != 24 || game.DeckSize != 24 {
		t.Fatalf("deck size = %d, constant = %d; want 24", deck.Len(), game.DeckSize)
	}
	counts := cardCounts(deck.Cards())
	tests := []struct {
		name string
		suit game.Suit
	}{
		{"clubs", game.Clubs},
		{"diamonds", game.Diamonds},
		{"hearts", game.Hearts},
		{"spades", game.Spades},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, rank := range []game.Rank{game.Nine, game.Ten, game.Jack, game.Queen, game.King, game.Ace} {
				card := game.Card{Rank: rank, Suit: tt.suit}
				if counts[card] != 1 {
					t.Errorf("card %+v occurs %d times; want 1", card, counts[card])
				}
			}
		})
	}
}

func TestShuffle(t *testing.T) {
	tests := []struct {
		name  string
		seed  int64
		dealt int
	}{
		{"full deck", 1, 0},
		{"different seed", 42, 0},
		{"negative seed", -1, 0},
		{"partially dealt", 42, 7},
		{"one remaining", 42, 23},
		{"empty", 42, 24},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deck, other := game.NewDeck(), game.NewDeck()
			if tt.dealt > 0 {
				for _, d := range []*game.Deck{deck, other} {
					if _, err := d.Deal([]int{tt.dealt}); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := deck.Cards()
			for _, d := range []*game.Deck{deck, other} {
				if err := d.Shuffle(rand.New(rand.NewSource(tt.seed))); err != nil {
					t.Fatal(err)
				}
			}
			after := deck.Cards()
			assertSameCards(t, before, after)
			if !slices.Equal(after, other.Cards()) {
				t.Error("identical seeds produced different orders")
			}
			if len(before) > 1 && slices.Equal(before, after) {
				t.Error("fixed test seed did not change card order")
			}
		})
	}
}

func TestShuffleNilGenerator(t *testing.T) {
	deck := game.NewDeck()
	before := deck.Cards()
	if err := deck.Shuffle(nil); !errors.Is(err, game.ErrInvalidRandomSource) {
		t.Fatalf("Shuffle(nil) error = %v; want ErrInvalidRandomSource", err)
	}
	if !slices.Equal(before, deck.Cards()) {
		t.Error("invalid shuffle changed deck")
	}
}

func TestShuffleDistinctSources(t *testing.T) {
	first, second := game.NewDeck(), game.NewDeck()
	if err := first.Shuffle(rand.New(rand.NewSource(1))); err != nil {
		t.Fatal(err)
	}
	if err := second.Shuffle(rand.New(rand.NewSource(42))); err != nil {
		t.Fatal(err)
	}
	if slices.Equal(first.Cards(), second.Cards()) {
		t.Error("different fixed sources produced the same shuffle")
	}
}

func TestDeal(t *testing.T) {
	maximumPlayers := make([]int, game.DeckSize)
	for player := range maximumPlayers {
		maximumPlayers[player] = 1
	}

	tests := []struct {
		name     string
		requests [][]int
	}{
		{"minimum players", [][]int{{1, 1}}},
		{"initial hands", [][]int{{1, 1, 1, 1}}},
		{"maximum players", [][]int{maximumPlayers}},
		{"unequal hands", [][]int{{1, 3, 5, 2}}},
		{"all cards", [][]int{{6, 6, 6, 6}}},
		{"single recipient", [][]int{{24}}},
		{"successive deals", [][]int{{1, 2}, {3, 4}, {14}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deck := game.NewDeck()
			if err := deck.Shuffle(rand.New(rand.NewSource(7))); err != nil {
				t.Fatal(err)
			}
			original := deck.Cards()
			seen := make(map[game.Card]bool)
			offset := 0
			for _, sizes := range tt.requests {
				hands, err := deck.Deal(sizes)
				if err != nil {
					t.Fatal(err)
				}
				if len(hands) != len(sizes) {
					t.Fatalf("got %d hands; want %d", len(hands), len(sizes))
				}
				for player, hand := range hands {
					if len(hand) != sizes[player] {
						t.Fatalf("player %d got %d cards; want %d", player, len(hand), sizes[player])
					}
					if !slices.Equal(hand, original[offset:offset+len(hand)]) {
						t.Errorf("player %d did not receive next cards in deck order", player)
					}
					offset += len(hand)
					for _, card := range hand {
						if seen[card] {
							t.Errorf("card %+v dealt more than once", card)
						}
						seen[card] = true
					}
				}
				if deck.Len() != len(original)-offset {
					t.Errorf("remaining cards = %d; want %d", deck.Len(), len(original)-offset)
				}
				if !slices.Equal(deck.Cards(), original[offset:]) {
					t.Error("remaining cards differ from undealt suffix")
				}
			}
			for _, card := range deck.Cards() {
				if seen[card] {
					t.Errorf("dealt card %+v remains in deck", card)
				}
				seen[card] = true
			}
			if len(seen) != 24 {
				t.Errorf("dealt and undealt cards contain %d unique cards; want 24", len(seen))
			}
		})
	}
}

func TestDealInvalid(t *testing.T) {
	tests := []struct {
		name  string
		dealt int
		sizes []int
		want  error
	}{
		{"nil players", 0, nil, game.ErrInvalidDeal},
		{"empty players", 0, []int{}, game.ErrInvalidDeal},
		{"zero size", 0, []int{0}, game.ErrInvalidDeal},
		{"negative size", 0, []int{-1}, game.ErrInvalidDeal},
		{"later zero size", 0, []int{2, 0}, game.ErrInvalidDeal},
		{"later negative size", 0, []int{2, -1}, game.ErrInvalidDeal},
		{"oversized hand", 0, []int{25}, game.ErrInsufficientCards},
		{"oversized total", 0, []int{12, 13}, game.ErrInsufficientCards},
		{"integer overflow", 0, []int{1, int(^uint(0) >> 1)}, game.ErrInsufficientCards},
		{"partially depleted", 20, []int{3, 2}, game.ErrInsufficientCards},
		{"empty deck", 24, []int{1}, game.ErrInsufficientCards},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deck := game.NewDeck()
			if tt.dealt > 0 {
				if _, err := deck.Deal([]int{tt.dealt}); err != nil {
					t.Fatal(err)
				}
			}
			before := deck.Cards()
			hands, err := deck.Deal(tt.sizes)
			if !errors.Is(err, tt.want) {
				t.Errorf("Deal(%v) error = %v; want %v", tt.sizes, err, tt.want)
			}
			if hands != nil {
				t.Error("failed deal returned hands")
			}
			if !slices.Equal(before, deck.Cards()) {
				t.Error("failed deal changed deck")
			}
		})
	}
}

func TestCardStorageOwnership(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T, *game.Deck)
	}{
		{"snapshot", func(t *testing.T, deck *game.Deck) {
			before := deck.Cards()
			snapshot := deck.Cards()
			snapshot[0] = game.Card{}
			snapshot = append(snapshot, game.Card{})
			if !slices.Equal(before, deck.Cards()) {
				t.Error("mutating snapshot changed deck")
			}
		}},
		{"hands", func(t *testing.T, deck *game.Deck) {
			hands, err := deck.Deal([]int{1, 2})
			if err != nil {
				t.Fatal(err)
			}
			remaining := deck.Cards()
			other := append([]game.Card(nil), hands[1]...)
			hands[0][0] = game.Card{}
			// Appending must not overwrite another hand or the remaining deck.
			hands[0] = append(hands[0], make([]game.Card, 24)...)
			if !slices.Equal(other, hands[1]) || !slices.Equal(remaining, deck.Cards()) {
				t.Error("mutating a hand changed other cards")
			}
			if err := deck.Shuffle(rand.New(rand.NewSource(1))); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(other, hands[1]) {
				t.Error("shuffling remaining deck changed a dealt hand")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.run(t, game.NewDeck()) })
	}
}

func TestZeroDeck(t *testing.T) {
	var deck game.Deck
	if deck.Len() != 0 || len(deck.Cards()) != 0 {
		t.Fatal("zero-value deck is not empty")
	}
	if err := deck.Shuffle(rand.New(rand.NewSource(1))); err != nil {
		t.Fatal(err)
	}
	if _, err := deck.Deal([]int{1}); !errors.Is(err, game.ErrInsufficientCards) {
		t.Fatalf("empty deal error = %v; want ErrInsufficientCards", err)
	}
}

func cardCounts(cards []game.Card) map[game.Card]int {
	counts := make(map[game.Card]int)
	for _, card := range cards {
		counts[card]++
	}
	return counts
}

func assertSameCards(t *testing.T, before, after []game.Card) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("shuffle changed card count from %d to %d", len(before), len(after))
	}
	beforeCounts, afterCounts := cardCounts(before), cardCounts(after)
	for card, count := range beforeCounts {
		if afterCounts[card] != count {
			t.Errorf("shuffle changed count for %+v from %d to %d", card, count, afterCounts[card])
		}
	}
}
