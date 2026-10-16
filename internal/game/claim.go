package game

import (
	"errors"
	"fmt"
)

// ErrInvalidCards indicates an invalid card or duplicate physical card.
var ErrInvalidCards = errors.New("invalid dealt cards")

// IsSatisfiedBy checks the exact claim against the union of all dealt cards.
// It accepts surplus matching cards and does not mutate cards. Each physical
// card must occur at most once.
func (b Bid) IsSatisfiedBy(cards []Card) (bool, error) {
	if err := b.Validate(); err != nil {
		return false, err
	}

	var counts [Ace + 1]int
	var suitCounts [Spades + 1]int
	var suitedCounts [Spades + 1][Ace + 1]int
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
		suitCounts[card.Suit]++
		suitedCounts[card.Suit][card.Rank]++
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
	case LowStraight:
		return hasStraight(counts, Nine), nil
	case HighStraight:
		return hasStraight(counts, Ten), nil
	case Flush:
		return suitCounts[b.Suit] >= 5, nil
	case StraightFlush:
		return hasStraight(suitedCounts[b.Suit], Nine), nil
	case RoyalStraightFlush:
		return hasStraight(suitedCounts[b.Suit], Ten), nil
	default:
		return false, fmt.Errorf("%w: unknown category %d", ErrInvalidBid, b.Category)
	}
}

func hasStraight(counts [Ace + 1]int, first Rank) bool {
	for rank := first; rank < first+5; rank++ {
		if counts[rank] == 0 {
			return false
		}
	}
	return true
}
