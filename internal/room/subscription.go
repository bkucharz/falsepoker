package room

import (
	"errors"

	"github.com/bkucharz/falsepoker/internal/game"
)

// SubscriberQueueCapacity bounds pending views per connection, including the
// initial snapshot. It is an infrastructure limit, not a gameplay rule.
const SubscriberQueueCapacity = 16

var (
	ErrSlowSubscriber     = errors.New("subscriber queue is full; reconnect and resynchronize")
	ErrConnectionLimit    = errors.New("membership connection limit reached")
	ErrSubscriptionClosed = errors.New("subscription is closed")
)

// Subscription represents one connection, not a seat or membership. Multiple
// subscriptions for the same member receive independently owned views. The room
// never modifies a view once queued. It owns and closes both channels.
type Subscription struct {
	room    *Room
	actor   game.PlayerID
	updates chan RoomView
	done    chan struct{}
	err     error // protected by room.mu
}

// Subscribe atomically registers a member's connection and queues its initial
// snapshot at the current revision. Later changes follow in revision order, so
// callers need no separate View call that could race with subscription setup.
func (r *Room) Subscribe(actor game.PlayerID) (*Subscription, error) {
	return r.SubscribeLimited(actor, r.id, 0)
}

// SubscribeLimited checks the room lifetime and per-member connection capacity
// atomically with registration. Zero limit is used by local room callers.
func (r *Room) SubscribeLimited(actor game.PlayerID, roomID string, limit int) (*Subscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settleLocked()
	if _, err := r.requireMember(actor); err != nil {
		return nil, err
	}
	if roomID != r.id {
		return nil, ErrStaleRoom
	}
	count := 0
	for sub := range r.subscribers {
		if sub.actor == actor {
			count++
		}
	}
	if limit > 0 && count >= limit {
		return nil, ErrConnectionLimit
	}
	r.cancelGraceLocked(actor)
	sub := &Subscription{
		room: r, actor: actor, updates: make(chan RoomView, SubscriberQueueCapacity),
		done: make(chan struct{}),
	}
	if r.subscribers == nil {
		r.subscribers = make(map[*Subscription]struct{})
	}
	r.subscribers[sub] = struct{}{}
	sub.updates <- r.viewLocked(actor)
	return sub, nil
}

// Updates contains ordered, detached views. Receiving code owns each view and
// may modify it; it must not share mutable data between its own goroutines.
func (s *Subscription) Updates() <-chan RoomView { return s.updates }

// Done closes when unsubscribed, on permanent departure/room deletion, or on
// overflow. A transport must stop writing and close its connection on Done;
// socket I/O runs outside the room lock/package.
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Revision returns authorized metadata only while the connection is registered.
func (s *Subscription) Revision() (uint64, error) {
	s.room.mu.Lock()
	defer s.room.mu.Unlock()
	s.room.settleLocked()
	if _, ok := s.room.subscribers[s]; !ok {
		return 0, ErrSubscriptionClosed
	}
	return s.room.revision, nil
}

// Err returns the terminal reason, or nil for an active/manually closed
// subscription. It is safe to call concurrently with commands and Close.
func (s *Subscription) Err() error {
	s.room.mu.Lock()
	defer s.room.mu.Unlock()
	return s.err
}

// Close is idempotent. Closing the final subscription starts reconnect grace;
// membership and participation remain intact until explicit leave or expiry.
func (s *Subscription) Close() {
	s.room.mu.Lock()
	defer s.room.mu.Unlock()
	s.room.closeSubscriptionLocked(s, nil)
}

// publishLocked commits one revision after a successful state change. Enqueue
// is nonblocking and independent for every subscription; a slow connection
// cannot block gameplay or another subscriber. No callbacks or socket writes run.
func (r *Room) publishLocked() {
	r.revision++
	for sub := range r.subscribers {
		if r.closed {
			r.closeSubscriptionLocked(sub, ErrRoomClosed)
			continue
		}
		select {
		case sub.updates <- r.viewLocked(sub.actor):
		default:
			r.closeSubscriptionLocked(sub, ErrSlowSubscriber)
		}
	}
}

func (r *Room) closeSubscriptionLocked(sub *Subscription, err error) {
	if _, exists := r.subscribers[sub]; !exists {
		return
	}
	delete(r.subscribers, sub)
	sub.err = err
	// Discard pending snapshots on termination; the connection must recover from
	// a fresh subscription instead of draining an obsolete backlog.
	close(sub.done)
	close(sub.updates)
	for range sub.updates {
	}
	r.startGraceLocked(sub.actor)
}
