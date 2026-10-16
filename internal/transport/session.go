package transport

import (
	"crypto/sha256"
	"sync"
	"time"

	"github.com/bkucharz/falsepoker/internal/game"
	"github.com/bkucharz/falsepoker/internal/room"
)

type session struct {
	mu          sync.Mutex
	player      game.PlayerID
	active      bool
	lastSeen    time.Time
	memberships map[string]*room.Room
	retries     map[string]retryRecord
	mutations   bucket
}

type retryRecord struct {
	fingerprint [32]byte
	until       time.Time
	response    response
}

func (s *session) prune(now time.Time) {
	hadMembership := len(s.memberships) > 0
	for id, r := range s.memberships {
		if _, err := r.Membership(s.player); err != nil {
			delete(s.memberships, id)
		}
	}
	if hadMembership && len(s.memberships) == 0 {
		s.lastSeen = now
	}
	for key, record := range s.retries {
		if !now.Before(record.until) {
			delete(s.retries, key)
		}
	}
}

// mutate serializes this session's HTTP ledger and membership admissions. The
// room captures its mutation result before returning; this code never holds a
// room lock while accessing transport state or writing to the network.
func (s *Server) mutate(sess *session, key, fingerprint string, apply func() response) response {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	now := s.now()
	sess.prune(now)
	if !sess.active {
		return errorResponse(key, fail(401, "SESSION_EXPIRED", "The session is unavailable."))
	}
	if ok, delay := sess.mutations.allow(now, s.limits.MutationRate, s.limits.MutationBurst); !ok {
		return errorResponse(key, transient(429, "RATE_LIMITED", "Too many mutations.", delay))
	}
	digest := sha256.Sum256([]byte(fingerprint))
	if record, exists := sess.retries[key]; exists {
		if digest != record.fingerprint {
			return errorResponse(key, fail(409, "REQUEST_ID_REUSED", "The request ID was used for different content."))
		}
		result := record.response
		result.replayed = true
		return result
	}
	if len(sess.retries) >= s.limits.MaxRetryRecords {
		delay := retryLifetime
		for _, record := range sess.retries {
			if left := record.until.Sub(now); left < delay {
				delay = left
			}
		}
		return errorResponse(key, transient(429, "RETRY_WINDOW_FULL", "Retry storage is full.", max(1, int(delay.Seconds())+1)))
	}
	// The session lock reserves the free cache slot until apply finishes.
	result := apply()
	if result.retryAfter == 0 {
		sess.retries[key] = retryRecord{fingerprint: digest, until: now.Add(retryLifetime), response: result}
	}
	return result
}
