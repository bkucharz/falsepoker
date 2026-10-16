package room

import (
	"errors"
	"fmt"
	"testing"

	"github.com/bkucharz/falsepoker/internal/game"
)

func TestMembershipResultsAndRoomLifetime(t *testing.T) {
	registry := deterministicRegistry()
	registry.newCode = func() (string, error) { return "same", nil }
	first, created, err := registry.CreateWithMembership("owner", " Owner ")
	if err != nil {
		t.Fatal(err)
	}
	if created.RoomID == "" || created.RoomID != first.ID() || created.Code != "same" || created.Revision != 1 || created.PlayerID != "owner" {
		t.Fatalf("incorrect creation result: %+v", created)
	}
	joined, changed, err := first.JoinWithMembership("guest", "Guest")
	if err != nil || !changed || joined.Revision != 2 {
		t.Fatal("join did not capture its committed revision")
	}
	again, changed, err := first.JoinWithMembership("guest", "New name")
	if err != nil || changed || again != joined {
		t.Fatal("idempotent join changed metadata")
	}
	if err := first.LeaveRoom("owner", "wrong"); !errors.Is(err, ErrStaleRoom) {
		t.Fatal("wrong room lifetime accepted")
	}
	if err := first.Leave("guest"); err != nil {
		t.Fatal(err)
	}
	if err := first.Leave("owner"); err != nil {
		t.Fatal(err)
	}
	next := createRoom(t, registry, "owner")
	if next.ID() == first.ID() {
		t.Fatal("reused invitation code reused room identity")
	}
	if err := next.LeaveRoom("owner", first.ID()); !errors.Is(err, ErrStaleRoom) {
		t.Fatal("delayed leave affected another room lifetime")
	}
	if _, err := next.Membership("outsider"); !errors.Is(err, ErrNotMember) {
		t.Fatal("metadata leaked to a nonmember")
	}
}

func TestRegistryAndMemberCapacity(t *testing.T) {
	for _, limits := range [][2]int{{0, 64}, {1, 23}, {-1, 24}} {
		if _, err := NewRegistryWithLimits(limits[0], limits[1]); err == nil {
			t.Fatal("invalid registry limits accepted")
		}
	}
	registry, err := NewRegistryWithLimits(1, 24)
	if err != nil {
		t.Fatal(err)
	}
	r := createRoom(t, registry, "owner")
	if _, err := registry.Create("other", "Other"); !errors.Is(err, ErrRegistryFull) {
		t.Fatal("registry capacity not enforced")
	}
	for i := 0; i < 23; i++ {
		if err := r.Join(game.PlayerID(fmt.Sprintf("p%d", i)), fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	before := lobby(t, r, "owner")
	if err := r.Join("extra", "Extra"); !errors.Is(err, ErrRoomFull) {
		t.Fatal("member capacity not enforced")
	}
	if lobby(t, r, "owner").Revision != before.Revision {
		t.Fatal("capacity rejection changed revision")
	}
	if _, changed, err := r.JoinWithMembership("owner", "Ignored"); err != nil || changed {
		t.Fatal("full room rejected an existing membership")
	}
}
