package room

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/bkucharz/falsepoker/internal/game"
)

type Member struct {
	ID          game.PlayerID
	DisplayName string
	Ready       bool
}

// Membership is detached metadata captured within a membership operation.
type Membership struct {
	RoomID   string
	Code     string
	PlayerID game.PlayerID
	Revision uint64
}

// LobbyState contains membership and public game metadata, with no cards or
// credentials. Participant order is the fixed game seat order, including former
// participants. Personalized gameplay views are a separate responsibility.
type LobbyState struct {
	Code         string
	Revision     uint64
	Admin        game.PlayerID
	Members      []Member
	GameID       uint64
	GamePhase    game.GamePhase
	Participants []game.PlayerID
}

// StartOptions explicitly orders all ready members or requests a random order.
// FirstBidder empty requests random selection from those participants.
type StartOptions struct {
	SeatOrder    []game.PlayerID
	ShuffleSeats bool
	FirstBidder  game.PlayerID
}

// Room owns membership, readiness, randomness, and its game. Create rooms through
// Registry.Create. No mutable game pointer escapes. A retained reference cannot
// reopen a room after deletion.
type Room struct {
	mu          sync.Mutex
	registry    *Registry
	code        string
	id          string
	maxMembers  int
	closed      bool
	admin       game.PlayerID
	members     []Member
	rng         *rand.Rand
	game        *game.Game
	gameID      uint64
	revision    uint64
	subscribers map[*Subscription]struct{}
	playerNames map[game.PlayerID]string
	retries     map[game.PlayerID]map[string]commandRecord
}

// Code is an immutable invitation identifier; it does not expose room state.
func (r *Room) Code() string { return r.code }

// ID identifies this room lifetime, even if its invitation code is later reused.
func (r *Room) ID() string { return r.id }

// Join adds an initially unready member. During play, new members are spectators.
// Room membership is not limited to the game's 24 participating players.
func (r *Room) Join(id game.PlayerID, displayName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.joinLocked(id, displayName)
}

// JoinWithMembership is idempotent for an existing member. It does not rename
// that member or reset readiness, and returns metadata under the same room lock.
func (r *Room) JoinWithMembership(id game.PlayerID, displayName string) (Membership, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	err := r.joinLocked(id, displayName)
	if err != nil && err != ErrAlreadyMember {
		return Membership{}, false, err
	}
	return r.membershipLocked(id), err == nil, nil
}

func (r *Room) joinLocked(id game.PlayerID, displayName string) error {
	if r.closed {
		return ErrRoomClosed
	}
	member, err := newMember(id, displayName)
	if err != nil {
		return err
	}
	if r.memberSeat(id) != -1 {
		return ErrAlreadyMember
	}
	if len(r.members) >= r.maxMembers {
		return ErrRoomFull
	}
	r.members = append(r.members, member)
	r.publishLocked()
	return nil
}

// SetReady changes readiness in the lobby or after a finished game, never during
// play. More than 24 members may be ready, but Start rejects that participant set.
func (r *Room) SetReady(actor game.PlayerID, ready bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.setReadyLocked(actor, ready)
}

func (r *Room) setReadyLocked(actor game.PlayerID, ready bool) error {
	seat, err := r.requireMember(actor)
	if err != nil {
		return err
	}
	if r.game != nil && r.game.State().Phase == game.GamePlaying {
		return ErrGameInProgress
	}
	if r.members[seat].Ready != ready {
		r.members[seat].Ready = ready
		r.publishLocked()
	}
	return nil
}

// Start atomically checks the ready set and admin's choices, then initializes a
// new game. Invalid configuration does not change state or consume randomness.
func (r *Room) Start(actor game.PlayerID, options StartOptions) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.startLocked(actor, options)
}

func (r *Room) startLocked(actor game.PlayerID, options StartOptions) error {
	if _, err := r.requireMember(actor); err != nil {
		return err
	}
	if actor != r.admin {
		return ErrNotAdmin
	}
	if r.game != nil && r.game.State().Phase == game.GamePlaying {
		return ErrGameInProgress
	}
	var ready []game.PlayerID
	for _, member := range r.members {
		if member.Ready {
			ready = append(ready, member.ID)
		}
	}
	if len(ready) < 2 || len(ready) > game.DeckSize {
		return fmt.Errorf("%w: expected 2–24 ready participants", ErrInvalidStart)
	}
	seats := slices.Clone(options.SeatOrder)
	if options.ShuffleSeats {
		if len(seats) != 0 {
			return fmt.Errorf("%w: specify seat order or shuffle, not both", ErrInvalidStart)
		}
		seats = slices.Clone(ready)
	} else {
		if len(seats) != len(ready) {
			return fmt.Errorf("%w: order must contain every ready member", ErrInvalidStart)
		}
		seen := make(map[game.PlayerID]bool, len(seats))
		for _, id := range seats {
			if seen[id] || !slices.Contains(ready, id) {
				return fmt.Errorf("%w: order must contain each ready member exactly once", ErrInvalidStart)
			}
			seen[id] = true
		}
	}
	if options.FirstBidder != "" && !slices.Contains(ready, options.FirstBidder) {
		return fmt.Errorf("%w: first bidder must be ready", ErrInvalidStart)
	}
	if options.ShuffleSeats {
		r.rng.Shuffle(len(seats), func(i, j int) { seats[i], seats[j] = seats[j], seats[i] })
	}
	starter := options.FirstBidder
	if starter == "" {
		starter = seats[r.rng.Intn(len(seats))]
	}
	next, err := game.NewGame(seats, starter, r.rng)
	if err != nil {
		return err
	}
	r.game = next
	r.playerNames = make(map[game.PlayerID]string, len(seats))
	for _, member := range r.members {
		if member.Ready {
			r.playerNames[member.ID] = member.DisplayName
		}
	}
	r.gameID++
	r.publishLocked()
	return nil
}

// Leave removes membership. An active participant is withdrawn from the game,
// while spectators/eliminated players can leave without changing gameplay.
// The final member closes the room and removes it from registry lookup.
func (r *Room) Leave(actor game.PlayerID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.leaveLocked(actor)
}

// LeaveRoom prevents a delayed leave from affecting a reused invitation code.
func (r *Room) LeaveRoom(actor game.PlayerID, roomID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.requireMember(actor); err != nil {
		return err
	}
	if roomID != r.id {
		return ErrStaleRoom
	}
	return r.leaveLocked(actor)
}

func (r *Room) leaveLocked(actor game.PlayerID) error {
	seat, err := r.requireMember(actor)
	if err != nil {
		return err
	}
	if r.game != nil {
		state := r.game.State()
		if state.Phase == game.GamePlaying {
			for _, player := range state.Players {
				if player.ID == actor && !player.Eliminated && !player.Withdrawn {
					if err := r.game.Withdraw(actor); err != nil {
						return err
					}
					if r.game.State().Phase == game.GameFinished {
						r.clearReadiness()
					}
					break
				}
			}
		}
	}
	r.members = slices.Delete(r.members, seat, seat+1)
	if len(r.members) == 0 {
		r.closed = true
		r.registry.remove(r)
	} else if actor == r.admin {
		r.admin = r.members[r.rng.Intn(len(r.members))].ID
	}
	for sub := range r.subscribers {
		if sub.actor == actor && !r.closed {
			r.closeSubscriptionLocked(sub, ErrNotMember)
		}
	}
	r.publishLocked()
	return nil
}

// Snapshot returns detached lobby metadata only to an existing member. It does
// not expose hands, the undealt deck, or an authoritative game pointer.
func (r *Room) Snapshot(actor game.PlayerID) (LobbyState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.requireMember(actor); err != nil {
		return LobbyState{}, err
	}
	state := LobbyState{Code: r.code, Revision: r.revision, Admin: r.admin, Members: slices.Clone(r.members), GameID: r.gameID}
	if r.game != nil {
		current := r.game.State()
		state.GamePhase = current.Phase
		for _, player := range current.Players {
			state.Participants = append(state.Participants, player.ID)
		}
	}
	return state, nil
}

func (r *Room) requireMember(actor game.PlayerID) (int, error) {
	if r.closed {
		return -1, ErrRoomClosed
	}
	seat := r.memberSeat(actor)
	if seat == -1 {
		return -1, ErrNotMember
	}
	return seat, nil
}

// Membership authorizes a metadata read without copying private game state.
func (r *Room) Membership(actor game.PlayerID) (Membership, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.requireMember(actor); err != nil {
		return Membership{}, err
	}
	return r.membershipLocked(actor), nil
}

func (r *Room) membershipLocked(actor game.PlayerID) Membership {
	return Membership{RoomID: r.id, Code: r.code, PlayerID: actor, Revision: r.revision}
}

func (r *Room) memberSeat(id game.PlayerID) int {
	for seat, member := range r.members {
		if member.ID == id {
			return seat
		}
	}
	return -1
}

func (r *Room) clearReadiness() {
	for seat := range r.members {
		r.members[seat].Ready = false
	}
}

func newMember(id game.PlayerID, displayName string) (Member, error) {
	displayName, err := NormalizeDisplayName(displayName)
	if id == "" || err != nil {
		return Member{}, ErrInvalidMember
	}
	return Member{ID: id, DisplayName: displayName}, nil
}

// NormalizeDisplayName defines room name validation shared with transport input.
func NormalizeDisplayName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 32 {
		return "", ErrInvalidMember
	}
	for _, ch := range name {
		if unicode.IsControl(ch) {
			return "", ErrInvalidMember
		}
	}
	return name, nil
}
