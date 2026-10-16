package room

import (
	"slices"

	"github.com/bkucharz/falsepoker/internal/game"
)

// ViewerRole describes the recipient's role in the current game, independently
// of admin privileges. Eliminated and withdrawn participants are spectators.
type ViewerRole uint8

const (
	Spectator ViewerRole = iota
	Participant
)

// RoomView is a detached, member-only snapshot suitable for a fresh client.
// Game is nil before the first game starts. Recipient is a public player ID,
// never an authentication credential.
type RoomView struct {
	Code      string
	Admin     game.PlayerID
	Members   []Member
	GameID    uint64
	Recipient game.PlayerID
	Role      ViewerRole
	Game      *GameView
}

// PlayerView preserves fixed game seat order, including former participants.
// HandCount counts actually dealt cards; RequiredHandSize can differ after a
// penalty in resolution. Hand is nil when hidden or when no cards were dealt.
type PlayerView struct {
	ID               game.PlayerID
	RequiredHandSize int
	HandCount        int
	Eliminated       bool
	Withdrawn        bool
	Hand             []game.Card
}

// GameView deliberately lists public fields instead of embedding GameState,
// which contains every private hand and the undealt deck. LastResult is the
// public completed-round reveal and remains available after a new deal.
type GameView struct {
	Phase            game.GamePhase
	RoundPhase       game.RoundPhase
	RoundID          uint64
	EliminationLimit int
	Starter          game.PlayerID
	NextStarter      game.PlayerID
	CurrentPlayer    game.PlayerID
	Players          []PlayerView
	LastBid          *game.PlacedBid
	LastResult       *game.RoundResult
	Winner           game.PlayerID
}

// View authorizes membership and builds a consistent recipient-specific view
// under the room lock. The caller must authenticate actor; knowing a player ID
// or invitation code is not authentication. Returned mutable data is owned by
// the caller and may be retained or changed without affecting later views.
// Spectator hands are authorized data; the future UI must hide them by default
// and show them only while its peek control is held.
func (r *Room) View(actor game.PlayerID) (RoomView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.requireMember(actor); err != nil {
		return RoomView{}, err
	}
	view := RoomView{
		Code: r.code, Admin: r.admin, Members: slices.Clone(r.members),
		GameID: r.gameID, Recipient: actor, Role: Spectator,
	}
	if r.game == nil {
		return view, nil
	}
	state := r.game.State()
	for _, player := range state.Players {
		if player.ID == actor && !player.Eliminated && !player.Withdrawn {
			view.Role = Participant
			break
		}
	}
	current := &GameView{
		Phase: state.Phase, RoundPhase: state.RoundPhase, RoundID: state.RoundID,
		EliminationLimit: state.EliminationLimit, Starter: state.Starter,
		NextStarter: state.NextStarter, CurrentPlayer: state.CurrentPlayer,
		Players: make([]PlayerView, len(state.Players)),
		LastBid: state.LastBid, LastResult: state.LastResult, Winner: state.Winner,
	}
	for seat, player := range state.Players {
		public := PlayerView{
			ID: player.ID, RequiredHandSize: player.HandSize, HandCount: len(player.Hand),
			Eliminated: player.Eliminated, Withdrawn: player.Withdrawn,
		}
		survivor := !player.Eliminated && !player.Withdrawn
		if state.RoundPhase == game.RoundResolution || (view.Role == Participant && player.ID == actor) || (view.Role == Spectator && survivor) {
			// Game.State already copied these cards and the bid/result above.
			public.Hand = player.Hand
		}
		current.Players[seat] = public
	}
	view.Game = current
	return view, nil
}
