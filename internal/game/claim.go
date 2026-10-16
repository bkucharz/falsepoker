package game

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidCards indicates an invalid card or duplicate physical card.
	ErrInvalidCards = errors.New("invalid dealt cards")
	// ErrUnsupportedBidCategory distinguishes an unevaluated category from a
	// claim that was evaluated and found false. Sequence and suit categories
	// will be supported in implementation-plan task 1.4.
	ErrUnsupportedBidCategory = errors.New("unsupported bid category")
)

// IsSatisfiedBy checks the exact claim against the union of all dealt cards.
// It accepts surplus matching cards and does not mutate cards. Each physical
// card must occur at most once. Currently, only rank-based categories are supported.
func (b Bid) IsSatisfiedBy(cards []Card) (bool, error) {
	if err := b.Validate(); err != nil {
		return false, err
	}

	var counts [Ace + 1]int
	seen := make(map[Card]struct{}, len(cards))
	for index, card := range cards {
		if !validRank(card.Rank) || card.Suit < Clubs || card.Suit > Spades {
			return false, fmt.Errorf("%w: card %d has invalid rank or suit", ErrInvalidCards, index)
		}
		if _, duplicate := seen[card]; duplicate {
			return false, fmt.Errorf("%w: duplicate card at index %d", ErrInvalidCards, index)
		}
		seen[card] = struct{}{}
		counts[card.Rank]++
	}

	switch b.Category {
	case HighCard:
		return counts[b.PrimaryRank] >= 1, nil
	case Pair:
		return counts[b.PrimaryRank] >= 2, nil
	case TwoPairs:
		return counts[b.PrimaryRank] >= 2 && counts[b.SecondaryRank] >= 2, nil
	case ThreeOfAKind:
		return counts[b.PrimaryRank] >= 3, nil
	case FullHouse:
		return counts[b.PrimaryRank] >= 3 && counts[b.SecondaryRank] >= 2, nil
	case FourOfAKind:
		return counts[b.PrimaryRank] >= 4, nil
	default:
		return false, fmt.Errorf("%w: %d", ErrUnsupportedBidCategory, b.Category)
	}
}
