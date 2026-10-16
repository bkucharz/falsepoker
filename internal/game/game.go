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
	ErrInactivePlayer = errors.New("participant is not active")
	ErrOutOfTurn      = errors.New("action is out of turn")
	ErrBidNotHigher   = errors.New("bid must be strictly higher")
	ErrNoBid          = errors.New("there is no bid to challenge")
)

// PlayerState describes a participant in the game's fixed seat order. HandSize
// is the required size for the next deal; Hand retains the just-played cards
// during resolution, even after a penalty changes HandSize or eliminates a player.
type PlayerState struct {
	ID         PlayerID
	HandSize   int
	Hand       []Card
	Eliminated bool
	Withdrawn  bool
}

// PlacedBid records both the claim and the participant who made it.
type PlacedBid struct {
	PlayerID PlayerID
	Bid      Bid
}

// RevealedHand preserves ownership of the cards dealt during a resolved round.
type RevealedHand struct {
	PlayerID PlayerID
	Cards    []Card
}

// RoundResult records a challenge using the cards and limit from that round.
type RoundResult struct {
	RoundID          uint64
	EliminationLimit int
	ChallengedBid    PlacedBid
	Challenger       PlayerID
	ClaimSatisfied   bool
	Loser            PlayerID
	RevealedHands    []RevealedHand
}

// GameState is a detached snapshot for trusted domain callers. It contains all
// hands and undealt cards; a transport must use recipient-specific views instead.
type GameState struct {
	Phase            GamePhase
	RoundPhase       RoundPhase
	RoundID          uint64
	EliminationLimit int
	Starter          PlayerID
	NextStarter      PlayerID
	CurrentPlayer    PlayerID
	Players          []PlayerState
	LastBid          *PlacedBid
	LastResult       *RoundResult
	Winner           PlayerID
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
	nextStarter      PlayerID
	players          []PlayerState
	turn             int
	lastBid          *PlacedBid
	lastResult       *RoundResult
	winner           PlayerID
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
		NextStarter:      g.nextStarter,
		Winner:           g.winner,
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
	if g.lastResult != nil {
		result := *g.lastResult
		result.RevealedHands = make([]RevealedHand, len(g.lastResult.RevealedHands))
		for i, hand := range g.lastResult.RevealedHands {
			result.RevealedHands[i] = RevealedHand{PlayerID: hand.PlayerID, Cards: append([]Card(nil), hand.Cards...)}
		}
		state.LastResult = &result
	}
	return state
}

// PlaceBid accepts an opening bid or strict raise by the current active player.
// It records the bidder and advances in seat order, skipping inactive players.
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
	g.turn = g.nextActiveSeat(g.turn)
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

// Challenge resolves the preceding bid against all cards dealt in this round,
// including withdrawn participants' cards. Validation/evaluation finishes before
// any mutation. Success retains the reveal and applies one penalty or elimination;
// repeated challenges are rejected by the resolution/finished phase guards.
func (g *Game) Challenge(actor PlayerID) error {
	if err := g.ValidateChallenge(actor); err != nil {
		return err
	}
	var cards []Card
	var revealed []RevealedHand
	for _, player := range g.players {
		if len(player.Hand) == 0 {
			continue
		}
		cards = append(cards, player.Hand...)
		revealed = append(revealed, RevealedHand{PlayerID: player.ID, Cards: append([]Card(nil), player.Hand...)})
	}
	satisfied, err := g.lastBid.Bid.IsSatisfiedBy(cards)
	if err != nil {
		return err
	}
	loser := g.lastBid.PlayerID
	if satisfied {
		loser = actor
	}
	loserSeat := -1
	for seat, player := range g.players {
		if player.ID == loser {
			loserSeat = seat
			break
		}
	}
	if loserSeat == -1 {
		return ErrUnknownPlayer
	}

	g.lastResult = &RoundResult{
		RoundID:          g.roundID,
		EliminationLimit: g.eliminationLimit,
		ChallengedBid:    *g.lastBid,
		Challenger:       actor,
		ClaimSatisfied:   satisfied,
		Loser:            loser,
		RevealedHands:    revealed,
	}
	player := &g.players[loserSeat]
	if !player.Eliminated && !player.Withdrawn {
		if player.HandSize+1 >= g.eliminationLimit {
			player.Eliminated = true
		} else {
			player.HandSize++
		}
	}
	g.roundPhase = RoundResolution
	if !g.finishIfOneSurvivor() {
		if player.Eliminated || player.Withdrawn {
			loserSeat = g.nextActiveSeat(loserSeat)
		}
		g.nextStarter = g.players[loserSeat].ID
	}
	return nil
}

// NextRound advances a resolved round only on its designated starter's action.
// It shuffles the full deck with caller-supplied randomness, deals survivors their
// required hand sizes, and retains the previous result. Failure leaves state
// unchanged; invalid actors/phases do not consume randomness.
func (g *Game) NextRound(actor PlayerID, rng *rand.Rand) error {
	if g.phase != GamePlaying || g.roundPhase != RoundResolution {
		return ErrWrongPhase
	}
	seat, err := g.activePlayerSeat(actor)
	if err != nil {
		return err
	}
	if actor != g.nextStarter {
		return ErrOutOfTurn
	}
	var sizes []int
	for _, player := range g.players {
		if !player.Eliminated && !player.Withdrawn {
			sizes = append(sizes, player.HandSize)
		}
	}
	deck := NewDeck()
	if err := deck.Shuffle(rng); err != nil {
		return err
	}
	hands, err := deck.Deal(sizes)
	if err != nil {
		return err
	}
	dealt := 0
	for i := range g.players {
		player := &g.players[i]
		player.Hand = nil
		if !player.Eliminated && !player.Withdrawn {
			player.Hand = hands[dealt]
			dealt++
		}
	}
	g.deck = deck
	g.eliminationLimit = min(6, DeckSize/len(sizes)+1)
	g.roundID++
	g.roundPhase = RoundBidding
	g.starter = actor
	g.nextStarter = ""
	g.turn = seat
	g.lastBid = nil
	return nil
}

// Withdraw permanently removes an active participant from this game. Room code
// calls this for explicit leave or expired reconnect grace, not temporary loss
// of connectivity. Cards and any existing bid remain in the current round.
func (g *Game) Withdraw(actor PlayerID) error {
	if g.phase != GamePlaying {
		return ErrWrongPhase
	}
	seat, err := g.activePlayerSeat(actor)
	if err != nil {
		return err
	}
	g.players[seat].Withdrawn = true
	if g.finishIfOneSurvivor() {
		return nil
	}
	if g.roundPhase == RoundBidding && g.turn == seat {
		g.turn = g.nextActiveSeat(seat)
	}
	if g.roundPhase == RoundResolution && g.nextStarter == actor {
		g.nextStarter = g.players[g.nextActiveSeat(seat)].ID
	}
	return nil
}

func (g *Game) nextActiveSeat(after int) int {
	for offset := 1; offset <= len(g.players); offset++ {
		seat := (after + offset) % len(g.players)
		if !g.players[seat].Eliminated && !g.players[seat].Withdrawn {
			return seat
		}
	}
	return -1
}

func (g *Game) finishIfOneSurvivor() bool {
	active := 0
	var survivor PlayerID
	for _, player := range g.players {
		if !player.Eliminated && !player.Withdrawn {
			active++
			survivor = player.ID
		}
	}
	if active != 1 {
		return false
	}
	g.phase = GameFinished
	g.roundPhase = RoundResolution
	g.nextStarter = ""
	g.winner = survivor
	return true
}

func (g *Game) activePlayerSeat(actor PlayerID) (int, error) {
	for seat, player := range g.players {
		if player.ID != actor {
			continue
		}
		if player.Eliminated || player.Withdrawn {
			return -1, ErrInactivePlayer
		}
		return seat, nil
	}
	return -1, ErrUnknownPlayer
}

func (g *Game) validateTurn(actor PlayerID) error {
	if g.phase != GamePlaying || g.roundPhase != RoundBidding {
		return ErrWrongPhase
	}
	seat, err := g.activePlayerSeat(actor)
	if err != nil {
		return err
	}
	if seat != g.turn {
		return ErrOutOfTurn
	}
	return nil
}
