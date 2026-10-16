package room

import (
	"errors"

	"github.com/bkucharz/falsepoker/internal/game"
)

var (
	ErrNoGame       = errors.New("no game has started")
	ErrStaleContext = errors.New("command does not target the current game and round")
)

// GameContext identifies the game and round observed by the caller. Neither
// field may be omitted; delayed commands must not act on a later round or game.
// Request IDs, retries, and expected-revision checks belong to the protocol.
type GameContext struct {
	GameID  uint64
	RoundID uint64
}

// PlaceBid applies an opening bid or strict raise as the authenticated member.
func (r *Room) PlaceBid(actor game.PlayerID, context GameContext, bid game.Bid) error {
	return r.gameCommand(actor, context, func(g *game.Game) error { return g.PlaceBid(actor, bid) })
}

// Challenge resolves the preceding bid once. A terminal challenge clears lobby
// readiness in the same operation, before any subscriber sees the finished game.
func (r *Room) Challenge(actor game.PlayerID, context GameContext) error {
	return r.gameCommand(actor, context, func(g *game.Game) error { return g.Challenge(actor) })
}

// NextRound advances resolution only for the designated next starter, using
// the room's private random generator to shuffle and redeal.
func (r *Room) NextRound(actor game.PlayerID, context GameContext) error {
	return r.gameCommand(actor, context, func(g *game.Game) error { return g.NextRound(actor, r.rng) })
}

func (r *Room) gameCommand(actor game.PlayerID, context GameContext, apply func(*game.Game) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.requireMember(actor); err != nil {
		return err
	}
	if r.game == nil {
		return ErrNoGame
	}
	state := r.game.State()
	if context.GameID != r.gameID || context.RoundID != state.RoundID {
		return ErrStaleContext
	}
	if err := apply(r.game); err != nil {
		return err
	}
	if r.game.State().Phase == game.GameFinished {
		r.clearReadiness()
	}
	r.publishLocked()
	return nil
}
