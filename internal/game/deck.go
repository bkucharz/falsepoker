package game

import (
	"errors"
	"fmt"
	"math/rand"
)

const DeckSize = 24

var (
	ErrInvalidDeal         = errors.New("deal requires players with positive hand sizes")
	ErrInsufficientCards   = errors.New("not enough cards in deck")
	ErrInvalidRandomSource = errors.New("random generator must not be nil")
)

// Deck owns the undealt cards. Its zero value is an empty deck.
// Like the rest of a game's mutable state, a deck must be used sequentially.
type Deck struct {
	cards []Card
}

// NewDeck returns all 24 unique cards in suit order, then ascending rank order.
func NewDeck() *Deck {
	cards := make([]Card, 0, DeckSize)
	for suit := Clubs; suit <= Spades; suit++ {
		for rank := Nine; rank <= Ace; rank++ {
			cards = append(cards, Card{Rank: rank, Suit: suit})
		}
	}
	return &Deck{cards: cards}
}

// Len returns the number of undealt cards.
func (d *Deck) Len() int {
	return len(d.cards)
}

// Cards returns a copy of the undealt cards in their current order.
func (d *Deck) Cards() []Card {
	return append([]Card(nil), d.cards...)
}

// Shuffle rearranges the undealt cards using the caller's random generator.
// Callers can inject a seeded source with rand.New(rand.NewSource(seed)).
func (d *Deck) Shuffle(rng *rand.Rand) error {
	if rng == nil {
		return ErrInvalidRandomSource
	}
	rng.Shuffle(len(d.cards), func(i, j int) {
		d.cards[i], d.cards[j] = d.cards[j], d.cards[i]
	})
	return nil
}

// Deal removes cards from the front of the deck and returns one hand per player,
// in the order of handSizes. Each hand receives consecutive cards and owns its
// storage. On failure, the deck is unchanged and no hands are returned.
func (d *Deck) Deal(handSizes []int) ([][]Card, error) {
	if len(handSizes) == 0 {
		return nil, ErrInvalidDeal
	}

	total := 0
	for player, size := range handSizes {
		if size <= 0 {
			return nil, fmt.Errorf("%w: player %d requested %d cards", ErrInvalidDeal, player, size)
		}
		// Subtraction avoids overflow even for arbitrarily large requested sizes.
		if size > len(d.cards)-total {
			return nil, fmt.Errorf("%w: player %d requested %d with %d remaining", ErrInsufficientCards, player, size, len(d.cards)-total)
		}
		total += size
	}

	hands := make([][]Card, len(handSizes))
	offset := 0
	for player, size := range handSizes {
		hands[player] = append([]Card(nil), d.cards[offset:offset+size]...)
		offset += size
	}
	d.cards = d.cards[total:]
	return hands, nil
}
