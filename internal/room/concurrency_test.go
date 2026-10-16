package room

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bkucharz/falsepoker/internal/game"
)

func TestConcurrentRoomCreation(t *testing.T) {
	registry := deterministicRegistry()
	created := make(chan *Room, 64)
	results := concurrent(64, func(i int) error {
		r, err := registry.Create(game.PlayerID(fmt.Sprintf("owner%d", i)), "Owner")
		if err != nil {
			return err
		}
		found, err := registry.Lookup(r.Code())
		if err != nil {
			return err
		}
		if found != r {
			return errors.New("concurrent lookup returned another room")
		}
		created <- r
		return nil
	})
	for _, err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	close(created)
	seen := make(map[string]bool)
	for r := range created {
		if seen[r.Code()] {
			t.Fatal("concurrent creation reused an active code")
		}
		seen[r.Code()] = true
		if found, err := registry.Lookup(r.Code()); err != nil || found != r {
			t.Fatal("created room missing or replaced")
		}
	}
	if len(seen) != 64 {
		t.Fatalf("created %d unique rooms; want 64", len(seen))
	}
}

func TestConcurrentJoinReadyReadAndStart(t *testing.T) {
	r := createRoom(t, deterministicRegistry(), "admin")
	joined := 0
	for _, err := range concurrent(100, func(i int) error {
		id := game.PlayerID(fmt.Sprintf("p%d", i%50))
		err := r.Join(id, "Player")
		if _, readErr := r.Snapshot("admin"); readErr != nil {
			return readErr
		}
		return err
	}) {
		if err == nil {
			joined++
		} else if !errors.Is(err, ErrAlreadyMember) {
			t.Fatal(err)
		}
	}
	if joined != 50 || len(lobby(t, r, "admin").Members) != 51 {
		t.Fatal("concurrent joins duplicated or lost identities")
	}
	for _, err := range concurrent(48, func(i int) error {
		return r.SetReady(game.PlayerID(fmt.Sprintf("p%d", i%24)), true)
	}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	started := 0
	for _, err := range concurrent(16, func(int) error {
		return r.Start("admin", StartOptions{ShuffleSeats: true})
	}) {
		if err == nil {
			started++
		} else if !errors.Is(err, ErrGameInProgress) {
			t.Fatal(err)
		}
	}
	state := lobby(t, r, "admin")
	if started != 1 || state.GameID != 1 || len(state.Participants) != 24 {
		t.Fatalf("concurrent starts exceeded capacity or replaced game: %+v", state)
	}
	seen := make(map[game.PlayerID]bool)
	for _, id := range state.Participants {
		if seen[id] {
			t.Fatal("participant has more than one seat")
		}
		seen[id] = true
	}
}

func TestConcurrentFinalLeaveAndJoin(t *testing.T) {
	for iteration := 0; iteration < 32; iteration++ {
		registry := deterministicRegistry()
		r := createRoom(t, registry, "admin")
		results := concurrent(2, func(i int) error {
			if i == 0 {
				return r.Leave("admin")
			}
			return r.Join("new", "New member")
		})
		for _, err := range results {
			if err != nil && !errors.Is(err, ErrRoomClosed) {
				t.Fatal(err)
			}
		}
		found, err := registry.Lookup(r.Code())
		if errors.Is(err, ErrRoomNotFound) {
			if joinErr := r.Join("late", "Late"); !errors.Is(joinErr, ErrRoomClosed) {
				t.Fatal("deleted room was resurrected")
			}
			continue
		}
		if err != nil || found != r {
			t.Fatalf("unexpected lookup: %v", err)
		}
		state := lobby(t, r, "new")
		if len(state.Members) != 1 || state.Admin != "new" {
			t.Fatalf("join-before-leave lost membership or admin: %+v", state)
		}
	}
}

func TestBusyRoomDoesNotBlockAnother(t *testing.T) {
	registry := deterministicRegistry()
	first, second := createRoom(t, registry, "a"), createRoom(t, registry, "b")
	first.mu.Lock()
	blocked := make(chan error, 1)
	go func() { blocked <- first.SetReady("a", true) }()
	done := make(chan error, 1)
	go func() {
		if _, err := registry.Lookup(second.Code()); err != nil {
			done <- err
			return
		}
		done <- second.Join("c", "C")
	}()
	var err error
	select {
	case err = <-done:
	case <-time.After(2 * time.Second):
		err = errors.New("unrelated room blocked by busy room")
	}
	first.mu.Unlock()
	if blockedErr := <-blocked; blockedErr != nil {
		t.Fatal(blockedErr)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func concurrent(count int, action func(int) error) []error {
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- action(i)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var collected []error
	for err := range results {
		collected = append(collected, err)
	}
	return collected
}
