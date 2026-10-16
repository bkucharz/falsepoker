// Package room coordinates in-memory membership and games independently of
// networking and storage. Caller-supplied identities must be authenticated by
// the eventual transport; a display name or invitation code is not an identity.
package room

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	mathrand "math/rand"
	"sync"

	"github.com/bkucharz/falsepoker/internal/game"
)

var (
	ErrRoomNotFound    = errors.New("room not found")
	ErrRoomClosed      = errors.New("room is closed")
	ErrCodeUnavailable = errors.New("could not allocate a unique room code")
	ErrInvalidMember   = errors.New("member identity and display name must be nonempty")
	ErrAlreadyMember   = errors.New("player is already a room member")
	ErrNotMember       = errors.New("player is not a room member")
	ErrNotAdmin        = errors.New("only the room admin may start a game")
	ErrGameInProgress  = errors.New("game is in progress")
	ErrInvalidStart    = errors.New("invalid game start options")
	ErrRoomFull        = errors.New("room member capacity reached")
	ErrRegistryFull    = errors.New("room registry capacity reached")
	ErrStaleRoom       = errors.New("room lifetime does not match")
)

const (
	DefaultMaxRooms   = 128
	DefaultMaxMembers = 64
)

// Registry owns room lookup and lifetime. Construct it with NewRegistry.
// Its lock never nests a room lock; individual rooms synchronize their own state.
type Registry struct {
	mu         sync.Mutex
	rooms      map[string]*Room
	newCode    func() (string, error)
	newSeed    func() (int64, error)
	newID      func() (string, error)
	maxRooms   int
	maxMembers int
}

func NewRegistry() *Registry {
	return newRegistry(DefaultMaxRooms, DefaultMaxMembers)
}

// NewRegistryWithLimits configures operational capacity, independently of the
// game's 24-participant limit. It must still permit a full 24-player game.
func NewRegistryWithLimits(maxRooms, maxMembers int) (*Registry, error) {
	if maxRooms < 1 || maxMembers < game.DeckSize {
		return nil, errors.New("room limits require positive room capacity and at least 24 members")
	}
	return newRegistry(maxRooms, maxMembers), nil
}

func newRegistry(maxRooms, maxMembers int) *Registry {
	return &Registry{
		rooms:    make(map[string]*Room),
		maxRooms: maxRooms, maxMembers: maxMembers,
		newID: func() (string, error) {
			var bytes [16]byte
			if _, err := rand.Read(bytes[:]); err != nil {
				return "", err
			}
			return "r_" + hex.EncodeToString(bytes[:]), nil
		},
		newCode: func() (string, error) {
			var bytes [6]byte
			if _, err := rand.Read(bytes[:]); err != nil {
				return "", err
			}
			return hex.EncodeToString(bytes[:]), nil
		},
		newSeed: func() (int64, error) {
			var bytes [8]byte
			if _, err := rand.Read(bytes[:]); err != nil {
				return 0, err
			}
			return int64(binary.LittleEndian.Uint64(bytes[:])), nil
		},
	}
}

// Create makes the creator the sole admin, initially not ready. Each room gets
// a separate random generator; code and seed generation are serialized here.
func (r *Registry) Create(creator game.PlayerID, displayName string) (*Room, error) {
	created, _, err := r.CreateWithMembership(creator, displayName)
	return created, err
}

// CreateWithMembership captures the creation result before making the room
// available to other operations; no subsequent authorized read is necessary.
func (r *Registry) CreateWithMembership(creator game.PlayerID, displayName string) (*Room, Membership, error) {
	member, err := newMember(creator, displayName)
	if err != nil {
		return nil, Membership{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.rooms) >= r.maxRooms {
		return nil, Membership{}, ErrRegistryFull
	}
	for attempt := 0; attempt < 16; attempt++ {
		code, err := r.newCode()
		if err != nil {
			return nil, Membership{}, fmt.Errorf("create invitation code: %w", err)
		}
		if code == "" {
			return nil, Membership{}, ErrCodeUnavailable
		}
		if _, exists := r.rooms[code]; exists {
			continue
		}
		seed, err := r.newSeed()
		if err != nil {
			return nil, Membership{}, fmt.Errorf("create room randomness: %w", err)
		}
		id, err := r.newID()
		if err != nil {
			return nil, Membership{}, fmt.Errorf("create room identity: %w", err)
		}
		room := &Room{
			registry: r,
			id:       id, maxMembers: r.maxMembers,
			code:     code,
			admin:    creator,
			revision: 1,
			members:  []Member{member},
			rng:      mathrand.New(mathrand.NewSource(seed)),
		}
		r.rooms[code] = room
		return room, room.membershipLocked(creator), nil
	}
	return nil, Membership{}, ErrCodeUnavailable
}

// Lookup is an in-process lookup for joining or authenticated coordination. It
// grants no access to room information; Snapshot separately requires membership.
func (r *Registry) Lookup(code string) (*Room, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	room, exists := r.rooms[code]
	if !exists {
		return nil, ErrRoomNotFound
	}
	return room, nil
}

// remove is called under the closing room's lock. Pointer identity prevents an
// old room reference from deleting another room if its code has been reused.
func (r *Registry) remove(room *Room) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rooms[room.code] == room {
		delete(r.rooms, room.code)
	}
}
