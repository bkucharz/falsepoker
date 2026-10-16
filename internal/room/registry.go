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
)

// Registry owns room lookup and lifetime. Construct it with NewRegistry.
// Its lock never nests a room lock; individual rooms synchronize their own state.
type Registry struct {
	mu      sync.Mutex
	rooms   map[string]*Room
	newCode func() (string, error)
	newSeed func() (int64, error)
}

func NewRegistry() *Registry {
	return &Registry{
		rooms: make(map[string]*Room),
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
	member, err := newMember(creator, displayName)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for attempt := 0; attempt < 16; attempt++ {
		code, err := r.newCode()
		if err != nil {
			return nil, fmt.Errorf("create invitation code: %w", err)
		}
		if code == "" {
			return nil, ErrCodeUnavailable
		}
		if _, exists := r.rooms[code]; exists {
			continue
		}
		seed, err := r.newSeed()
		if err != nil {
			return nil, fmt.Errorf("create room randomness: %w", err)
		}
		room := &Room{
			registry: r,
			code:     code,
			admin:    creator,
			revision: 1,
			members:  []Member{member},
			rng:      mathrand.New(mathrand.NewSource(seed)),
		}
		r.rooms[code] = room
		return room, nil
	}
	return nil, ErrCodeUnavailable
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
