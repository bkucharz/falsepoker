// Package game implements the gameplay domain independently of transport and storage.
package game

// Rank is a card's value, ordered from nine to ace.
type Rank uint8

const (
	Nine Rank = 9 + iota
	Ten
	Jack
	Queen
	King
	Ace
)

// Suit identifies one of the four suits. Suits have no gameplay precedence.
type Suit uint8

const (
	Clubs Suit = iota + 1
	Diamonds
	Hearts
	Spades
)

// Card identifies a physical card by its rank and suit.
type Card struct {
	Rank Rank
	Suit Suit
}
