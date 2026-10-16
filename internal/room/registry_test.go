package room

import (
	"errors"
	"fmt"
	"testing"

	"github.com/bkucharz/falsepoker/internal/game"
)

func TestRegistryCreateAndLookup(t *testing.T) {
	registry := NewRegistry()
	first, err := registry.Create("a", " Alice ")
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Create("b", "Bob")
	if err != nil {
		t.Fatal(err)
	}
	if first.Code() == second.Code() || len(first.Code()) != 12 {
		t.Fatalf("invalid invitation codes: %q, %q", first.Code(), second.Code())
	}
	found, err := registry.Lookup(first.Code())
	if err != nil || found != first {
		t.Fatalf("Lookup = (%v, %v); want original room", found, err)
	}
	state := lobby(t, first, "a")
	if state.Admin != "a" || len(state.Members) != 1 || state.Members[0].DisplayName != "Alice" || state.Members[0].Ready || state.GamePhase != game.GameUninitialized || state.GameID != 0 {
		t.Fatalf("unexpected initial lobby: %+v", state)
	}
	if _, err := registry.Lookup("unknown"); !errors.Is(err, ErrRoomNotFound) {
		t.Fatalf("unknown lookup error = %v", err)
	}
}

func TestRegistryCodeCollision(t *testing.T) {
	registry := deterministicRegistry()
	codes := []string{"same", "same", "different"}
	index := 0
	registry.newCode = func() (string, error) {
		code := codes[index]
		index++
		return code, nil
	}
	first := createRoom(t, registry, "a")
	second := createRoom(t, registry, "b")
	if first.Code() != "same" || second.Code() != "different" || index != 3 {
		t.Fatal("collision did not retry without overwriting original room")
	}
	found, err := registry.Lookup("same")
	if err != nil || found != first {
		t.Fatal("collision overwrote original room")
	}
}

func TestRegistryGenerationErrors(t *testing.T) {
	sourceError := errors.New("entropy unavailable")
	tests := []struct {
		name  string
		setup func(*Registry)
		want  error
	}{
		{"code source error", func(r *Registry) {
			r.newCode = func() (string, error) { return "", sourceError }
		}, sourceError},
		{"empty generated code", func(r *Registry) {
			r.newCode = func() (string, error) { return "", nil }
		}, ErrCodeUnavailable},
		{"seed source error", func(r *Registry) {
			r.newSeed = func() (int64, error) { return 0, sourceError }
		}, sourceError},
		{"collision exhaustion", func(r *Registry) {
			r.newCode = func() (string, error) { return "same", nil }
			createRoom(t, r, "existing")
		}, ErrCodeUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := deterministicRegistry()
			tt.setup(registry)
			before := len(registry.rooms)
			created, err := registry.Create("a", "Alice")
			if created != nil || !errors.Is(err, tt.want) || len(registry.rooms) != before {
				t.Fatalf("Create = (%v, %v); expected atomic failure %v", created, err, tt.want)
			}
		})
	}
}

func TestRegistryInvalidCreator(t *testing.T) {
	for _, tt := range []struct {
		name, display string
		id            game.PlayerID
	}{{"missing identity", "Alice", ""}, {"missing name", "", "a"}, {"blank name", " \t\n", "a"}} {
		t.Run(tt.name, func(t *testing.T) {
			registry := deterministicRegistry()
			registry.newCode = func() (string, error) { t.Fatal("invalid creator consumed entropy"); return "", nil }
			created, err := registry.Create(tt.id, tt.display)
			if created != nil || !errors.Is(err, ErrInvalidMember) {
				t.Fatalf("Create = (%v, %v); want invalid member", created, err)
			}
		})
	}
}

func TestRoomDeletionAndCodeReuse(t *testing.T) {
	registry := deterministicRegistry()
	registry.newCode = func() (string, error) { return "same", nil }
	old := createRoom(t, registry, "a")
	if err := old.Leave("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Lookup(old.Code()); !errors.Is(err, ErrRoomNotFound) {
		t.Fatal("empty room remains in registry")
	}
	for _, action := range []func() error{
		func() error { return old.Join("b", "Bob") },
		func() error { return old.SetReady("a", true) },
		func() error { return old.Start("a", StartOptions{ShuffleSeats: true}) },
		func() error { return old.Leave("a") },
		func() error { _, err := old.Snapshot("a"); return err },
	} {
		if err := action(); !errors.Is(err, ErrRoomClosed) {
			t.Fatalf("retained closed reference error = %v", err)
		}
	}
	next := createRoom(t, registry, "b")
	registry.remove(old)
	if found, err := registry.Lookup("same"); err != nil || found != next {
		t.Fatal("stale deletion removed a reused invitation code")
	}
}

func deterministicRegistry() *Registry {
	registry := NewRegistry()
	next := 0
	registry.newCode = func() (string, error) {
		next++
		return fmt.Sprintf("code-%d", next), nil
	}
	registry.newSeed = func() (int64, error) { return 42, nil }
	return registry
}

func createRoom(t *testing.T, registry *Registry, owner game.PlayerID) *Room {
	t.Helper()
	r, err := registry.Create(owner, string(owner))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func lobby(t *testing.T, r *Room, actor game.PlayerID) LobbyState {
	t.Helper()
	state, err := r.Snapshot(actor)
	if err != nil {
		t.Fatal(err)
	}
	return state
}
