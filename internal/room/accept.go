package room

import (
	"errors"
	"time"

	"github.com/bkucharz/falsepoker/internal/game"
)

const CommandRetryCapacity = 256
const CommandRetryLifetime = 5 * time.Minute

var (
	ErrStaleGame       = errors.New("game identity has changed")
	ErrStaleRound      = errors.New("round identity has changed")
	ErrStaleRevision   = errors.New("room revision has changed")
	ErrRequestIDReused = errors.New("request ID used for different content")
	ErrRetryWindowFull = errors.New("command retry storage is full")
	ErrUnknownCommand  = errors.New("unknown command")
)

// Command is validated intent from an authenticated adapter, never a client
// actor identity. Fingerprint identifies the normalized, complete envelope.
// Network encodings and socket types stay outside room coordination.
type Command struct {
	RequestID        string
	Fingerprint      [32]byte
	RoomID           string
	ExpectedRevision uint64
	Context          GameContext
	Action           string
	Ready            bool
	Start            StartOptions
	Bid              game.Bid
}

type CommandResult struct {
	Revision   uint64
	Changed    bool
	Replayed   bool
	Err        error
	RetryAfter time.Duration
}

type commandRecord struct {
	fingerprint [32]byte
	until       time.Time
	result      CommandResult
}

// Execute serializes authorization, retry admission, optimistic concurrency,
// mutation, publication and outcome retention. A closed connection cannot act.
// Time is supplied for deterministic retention tests; it is server controlled.
func (s *Subscription) Execute(command Command, now time.Time) CommandResult {
	r := s.room
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settleLocked()
	result := CommandResult{Revision: r.revision}
	if _, exists := r.subscribers[s]; !exists {
		result.Err = ErrSubscriptionClosed
		return result
	}
	if _, err := r.requireMember(s.actor); err != nil {
		result.Err = err
		return result
	}
	// Bound retained records even when many identities join and leave within
	// one retry window. Do not evict an unexpired outcome to make room.
	total := 0
	delay := CommandRetryLifetime
	for actor, records := range r.retries {
		for id, record := range records {
			if !now.Before(record.until) {
				delete(records, id)
				continue
			}
			total++
			if left := record.until.Sub(now); left < delay {
				delay = left
			}
		}
		if len(records) == 0 {
			delete(r.retries, actor)
		}
	}
	records := r.retries[s.actor]
	if record, exists := records[command.RequestID]; exists {
		if record.fingerprint != command.Fingerprint {
			result.Err = ErrRequestIDReused
			return result
		}
		result = record.result
		result.Replayed = true
		return result
	}
	if len(records) >= CommandRetryCapacity || total >= r.maxMembers*CommandRetryCapacity {
		if len(records) >= CommandRetryCapacity {
			delay = CommandRetryLifetime
			for _, record := range records {
				if left := record.until.Sub(now); left < delay {
					delay = left
				}
			}
		}
		result.Err, result.RetryAfter = ErrRetryWindowFull, delay
		return result
	}
	roundID := uint64(0)
	if r.game != nil {
		roundID = r.game.State().RoundID
	}
	switch {
	case command.RoomID != r.id:
		result.Err = ErrStaleRoom
	case command.Context.GameID != r.gameID:
		result.Err = ErrStaleGame
	case command.Context.RoundID != roundID:
		result.Err = ErrStaleRound
	case command.ExpectedRevision != r.revision:
		result.Err = ErrStaleRevision
	default:
		switch command.Action {
		case "ready":
			result.Err = r.setReadyLocked(s.actor, command.Ready)
		case "start":
			result.Err = r.startLocked(s.actor, command.Start)
		case "bid":
			result.Err = r.gameCommandLocked(s.actor, command.Context, func(g *game.Game) error { return g.PlaceBid(s.actor, command.Bid) })
		case "challenge":
			result.Err = r.gameCommandLocked(s.actor, command.Context, func(g *game.Game) error { return g.Challenge(s.actor) })
		case "next_round":
			result.Err = r.gameCommandLocked(s.actor, command.Context, func(g *game.Game) error { return g.NextRound(s.actor, r.rng) })
		default:
			result.Err = ErrUnknownCommand
		}
	}
	result.Changed = r.revision != result.Revision
	result.Revision = r.revision
	if r.retries == nil {
		r.retries = make(map[game.PlayerID]map[string]commandRecord)
	}
	if records == nil {
		records = make(map[string]commandRecord)
		r.retries[s.actor] = records
	}
	records[command.RequestID] = commandRecord{command.Fingerprint, now.Add(CommandRetryLifetime), result}
	return result
}
