package game

import (
	"errors"
	"fmt"
)

// BidCategory identifies a claim, ordered from weakest to strongest according to
// False Poker's rules. In particular, Flush ranks above FullHouse.
type BidCategory uint8

const (
	HighCard BidCategory = iota + 1
	Pair
	TwoPairs
	LowStraight
	HighStraight
	ThreeOfAKind
	FullHouse
	Flush
	FourOfAKind
	StraightFlush
	RoyalStraightFlush
)

// ErrInvalidBid indicates an unknown category or invalid category parameters.
var ErrInvalidBid = errors.New("invalid bid")

// Bid describes a claim, independently of whether it exists among dealt cards.
// PrimaryRank is the claimed rank, triple rank, or higher pair rank.
// SecondaryRank is the full-house pair rank or lower pair rank.
// Suit is required only for flush categories. Unused parameters must be zero.
type Bid struct {
	Category      BidCategory
	PrimaryRank   Rank
	SecondaryRank Rank
	Suit          Suit
}

// Validate checks the category and its exact required parameters. Two-pair ranks
// must already be ordered higher first; invalid bids are not silently normalized.
func (b Bid) Validate() error {
	switch b.Category {
	case HighCard, Pair, ThreeOfAKind, FourOfAKind:
		if !validRank(b.PrimaryRank) || b.SecondaryRank != 0 || b.Suit != 0 {
			return fmt.Errorf("%w: category %d requires exactly one rank", ErrInvalidBid, b.Category)
		}
	case FullHouse, TwoPairs:
		if !validRank(b.PrimaryRank) || !validRank(b.SecondaryRank) || b.Suit != 0 {
			return fmt.Errorf("%w: category %d requires exactly two ranks", ErrInvalidBid, b.Category)
		}
		if b.PrimaryRank == b.SecondaryRank {
			return fmt.Errorf("%w: ranks must be distinct", ErrInvalidBid)
		}
		if b.Category == TwoPairs && b.PrimaryRank < b.SecondaryRank {
			return fmt.Errorf("%w: two-pair ranks must be higher first", ErrInvalidBid)
		}
	case Flush, StraightFlush, RoyalStraightFlush:
		if b.Suit < Clubs || b.Suit > Spades || b.PrimaryRank != 0 || b.SecondaryRank != 0 {
			return fmt.Errorf("%w: category %d requires exactly one suit", ErrInvalidBid, b.Category)
		}
	case LowStraight, HighStraight:
		if b.PrimaryRank != 0 || b.SecondaryRank != 0 || b.Suit != 0 {
			return fmt.Errorf("%w: category %d takes no parameters", ErrInvalidBid, b.Category)
		}
	default:
		return fmt.Errorf("%w: unknown category %d", ErrInvalidBid, b.Category)
	}
	return nil
}

// IsHigherThan reports whether b is a strict raise over previous. Both bids must
// be valid. Suits are ignored for ordering, so changing only a suit is not a raise.
func (b Bid) IsHigherThan(previous Bid) (bool, error) {
	if err := b.Validate(); err != nil {
		return false, fmt.Errorf("new bid: %w", err)
	}
	if err := previous.Validate(); err != nil {
		return false, fmt.Errorf("previous bid: %w", err)
	}
	if b.Category != previous.Category {
		return b.Category > previous.Category, nil
	}
	if b.PrimaryRank != previous.PrimaryRank {
		return b.PrimaryRank > previous.PrimaryRank, nil
	}
	return b.SecondaryRank > previous.SecondaryRank, nil
}

func validRank(rank Rank) bool {
	return rank >= Nine && rank <= Ace
}
