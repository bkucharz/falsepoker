package game

import (
	"errors"
	"fmt"
	"math/rand"
)

// PlayerID is a stable participant identity supplied by the caller.
type PlayerID string

type GamePhase uint8

const (
	GameUninitialized GamePhase = iota
	GamePlaying
	GameFinished
)

type RoundPhase uint8

const (
	RoundUninitialized RoundPhase = iota
	RoundBidding
	RoundResolution
)

var (
	ErrInvalidGame    = errors.New("invalid game configuration")
	ErrWrongPhase     = errors.New("action is not allowed in the current phase")
	ErrUnknownPlayer  = errors.New("unknown participant")
	ErrInactivePlayer = errors.New("participant is eliminated")
	ErrOutOfTurn      = errors.New("action is out of turn")
	ErrBidNotHigher   = errors.New("bid must be strictly higher")
	ErrNoBid          = errors.New("there is no bid to challenge")
)

// PlayerState describes a participant in the game's fixed seat order.
type PlayerState struct {
	ID         PlayerID
	HandSize   int
	Hand       []Card
	Eliminated bool
}

// PlacedBid records both the claim and the participant who made it.
type PlacedBid struct {
	PlayerID PlayerID
	Bid      Bid
}

// GameState is a detached snapshot for trusted domain callers. It contains all
// hands and undealt cards; a transport must use recipient-specific views instead.
type GameState struct {
	Phase            GamePhase
	RoundPhase       RoundPhase
	RoundID          uint64
	EliminationLimit int
	Starter          PlayerID
	CurrentPlayer    PlayerID
	Players          []PlayerState
	LastBid          *PlacedBid
	Undealt          []Card
}

// Game owns mutable domain state and must be used sequentially. Its zero value
// is uninitialized and rejects gameplay actions.
type Game struct {
	phase            GamePhase
	roundPhase       RoundPhase
	roundID          uint64
	eliminationLimit int
	starter          PlayerID
	players          []PlayerState
	turn             int
	lastBid          *PlacedBid
	deck             *Deck
}

// NewGame starts round one with 2–24 distinct participants in the supplied seat
// order. The caller selects starter (explicitly or randomly) before this call.
// All configuration is checked before consuming randomness or dealing cards.
// Room membership, readiness, and admin permissions belong to room coordination.
func NewGame(seats []PlayerID, starter PlayerID, rng *rand.Rand) (*Game, error) {
	if len(seats) < 2 || len(seats) > DeckSize {
		return nil, fmt.Errorf("%w: expected 2–24 participants", ErrInvalidGame)
	}
	seen := make(map[PlayerID]bool, len(seats))
	turn := -1
	for seat, id := range seats {
		if id == "" || seen[id] {
			return nil, fmt.Errorf("%w: participant IDs must be nonempty and unique", ErrInvalidGame)
		}
		seen[id] = true
		if id == starter {
			turn = seat
		}
	}
	if turn == -1 {
		return nil, fmt.Errorf("%w: starter must be a participant", ErrInvalidGame)
	}
	if rng == nil {
		return nil, ErrInvalidRandomSource
	}

	deck := NewDeck()
	if err := deck.Shuffle(rng); err != nil {
		return nil, err
	}
	sizes := make([]int, len(seats))
	for seat := range sizes {
		sizes[seat] = 1
	}
	hands, err := deck.Deal(sizes)
	if err != nil {
		return nil, err
	}
	players := make([]PlayerState, len(seats))
	for seat, id := range seats {
		players[seat] = PlayerState{ID: id, HandSize: 1, Hand: hands[seat]}
	}
	return &Game{
		phase:            GamePlaying,
		roundPhase:       RoundBidding,
		roundID:          1,
		eliminationLimit: min(6, DeckSize/len(seats)+1),
		starter:          starter,
		players:          players,
		turn:             turn,
		deck:             deck,
	}, nil
}

// State returns an independent copy of the game's state, including card storage.
func (g *Game) State() GameState {
	state := GameState{
		Phase:            g.phase,
		RoundPhase:       g.roundPhase,
		RoundID:          g.roundID,
		EliminationLimit: g.eliminationLimit,
		Starter:          g.starter,
		Players:          make([]PlayerState, len(g.players)),
	}
	for seat, player := range g.players {
		state.Players[seat] = player
		state.Players[seat].Hand = append([]Card(nil), player.Hand...)
	}
	if g.phase == GamePlaying && g.roundPhase == RoundBidding {
		state.CurrentPlayer = g.players[g.turn].ID
	}
	if g.lastBid != nil {
		bid := *g.lastBid
		state.LastBid = &bid
	}
	if g.deck != nil {
		state.Undealt = g.deck.Cards()
	}
	return state
}

// PlaceBid accepts an opening bid or strict raise by the current active player.
// It records the bidder and advances in seat order, skipping eliminated players.
// Rejected actions leave state unchanged. Claims need not actually exist to bid.
func (g *Game) PlaceBid(actor PlayerID, bid Bid) error {
	if err := g.validateTurn(actor); err != nil {
		return err
	}
	if err := bid.Validate(); err != nil {
		return err
	}
	if g.lastBid != nil {
		higher, err := bid.IsHigherThan(g.lastBid.Bid)
		if err != nil {
			return err
		}
		if !higher {
			return ErrBidNotHigher
		}
	}
	g.lastBid = &PlacedBid{PlayerID: actor, Bid: bid}
	for offset := 1; offset <= len(g.players); offset++ {
		next := (g.turn + offset) % len(g.players)
		if !g.players[next].Eliminated {
			g.turn = next
			break
		}
	}
	return nil
}

// ValidateChallenge checks turn/phase eligibility and that an opening bid exists.
// It does not evaluate a claim or change state; challenge resolution is separate.
func (g *Game) ValidateChallenge(actor PlayerID) error {
	if err := g.validateTurn(actor); err != nil {
		return err
	}
	if g.lastBid == nil {
		return ErrNoBid
	}
	return nil
}

func (g *Game) validateTurn(actor PlayerID) error {
	if g.phase != GamePlaying || g.roundPhase != RoundBidding {
		return ErrWrongPhase
	}
	for seat, player := range g.players {
		if player.ID != actor {
			continue
		}
		if player.Eliminated {
			return ErrInactivePlayer
		}
		if seat != g.turn {
			return ErrOutOfTurn
		}
		return nil
	}
	return ErrUnknownPlayer
}
