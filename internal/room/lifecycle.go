package room

import (
	"errors"
	"time"

	"github.com/bkucharz/falsepoker/internal/game"
)

const ReconnectGrace = 60 * time.Second

// Clock supplies server-controlled monotonic time and cancellable callbacks.
// AfterFunc must schedule asynchronously, like time.AfterFunc; a Timer whose
// Stop returns true must never invoke its callback. No game rule uses this clock.
type Clock interface {
	Now() time.Time
	AfterFunc(time.Duration, func()) Timer
}

type Timer interface{ Stop() bool }

// gracePeriod identity is a generation token. A later disconnect or membership
// gets a different object, so already-running old callbacks cannot remove it.
type gracePeriod struct {
	deadline time.Time
	timer    Timer
}

// EnableReconnectGrace transfers lifecycle ownership to the server before any
// rooms exist. Local domain callers may use a registry without timed lifetimes.
func (r *Registry) EnableReconnectGrace(clock Clock) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if clock == nil || r.closed || r.clock != nil || len(r.rooms) != 0 {
		return errors.New("reconnect lifecycle requires a clock and an unused, open registry")
	}
	r.clock = clock
	return nil
}

func (r *Room) startGraceLocked(actor game.PlayerID) {
	if r.clock == nil || r.closed || r.memberSeat(actor) == -1 || r.grace[actor] != nil {
		return
	}
	for sub := range r.subscribers {
		if sub.actor == actor {
			return
		}
	}
	if r.grace == nil {
		r.grace = make(map[game.PlayerID]*gracePeriod)
	}
	period := &gracePeriod{deadline: r.clock.Now().Add(ReconnectGrace)}
	r.grace[actor] = period
	r.scheduleGraceLocked(actor, period)
}

func (r *Room) scheduleGraceLocked(actor game.PlayerID, period *gracePeriod) {
	r.registry.timerWG.Add(1)
	period.timer = r.clock.AfterFunc(max(0, period.deadline.Sub(r.clock.Now())), func() {
		defer r.registry.timerWG.Done()
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.closed || r.grace[actor] != period {
			return
		}
		// Real timers do not run early; retain the deadline if a supplied
		// scheduler wakes early rather than ever withdrawing a member early.
		if r.clock.Now().Before(period.deadline) {
			r.scheduleGraceLocked(actor, period)
			return
		}
		// The existing departure path owns withdrawal, admin transfer, a possible
		// win, revision assignment, publication and final room deletion.
		// This cannot fail: the period identifies an existing member and the
		// room exclusively owns a valid game, satisfying Withdraw's preconditions.
		_ = r.leaveLocked(actor)
	})
}

func (r *Room) cancelGraceLocked(actor game.PlayerID) {
	period := r.grace[actor]
	if period == nil {
		return
	}
	delete(r.grace, actor)
	if period.timer.Stop() {
		r.registry.timerWG.Done()
	}
}

// settleLocked makes deadlines authoritative even if their timer callback is
// waiting to run. It is called before reads, attachments and mutations. Member
// order makes simultaneous overdue departures reproducible; each leaves once.
func (r *Room) settleLocked() {
	if r.clock == nil || r.closed {
		return
	}
	now := r.clock.Now()
	var expired []game.PlayerID
	for _, member := range r.members {
		if period := r.grace[member.ID]; period != nil && !now.Before(period.deadline) {
			expired = append(expired, member.ID)
		}
	}
	for _, actor := range expired {
		// IDs came from current membership under this same lock; the room owns
		// a valid game, so the normal departure preconditions are satisfied.
		_ = r.leaveLocked(actor)
	}
}

// Close cancels all membership timers, closes subscriptions and discards rooms.
// It waits for already-running callbacks outside registry/room locks. A closed
// registry cannot create rooms; retained room references cannot reopen them.
func (r *Registry) Close() {
	r.closeOnce.Do(r.close)
}

func (r *Registry) close() {
	r.mu.Lock()
	r.closed = true
	rooms := make([]*Room, 0, len(r.rooms))
	for _, current := range r.rooms {
		rooms = append(rooms, current)
	}
	clear(r.rooms)
	r.mu.Unlock()
	for _, current := range rooms {
		current.mu.Lock()
		current.closed = true
		for actor := range current.grace {
			current.cancelGraceLocked(actor)
		}
		for sub := range current.subscribers {
			current.closeSubscriptionLocked(sub, ErrRoomClosed)
		}
		current.retries, current.playerNames, current.game, current.members = nil, nil, nil, nil
		current.mu.Unlock()
	}
	r.timerWG.Wait()
}
