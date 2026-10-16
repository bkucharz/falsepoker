package room

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"testing"

	"github.com/bkucharz/falsepoker/internal/game"
)

func TestJoinAndReady(t *testing.T) {
	r := createRoom(t, deterministicRegistry(), "admin")
	if err := r.Join("a", " Same name "); err != nil {
		t.Fatal(err)
	}
	if err := r.Join("b", "Same name"); err != nil {
		t.Fatal(err)
	}
	if err := r.SetReady("a", true); err != nil {
		t.Fatal(err)
	}
	state := lobby(t, r, "a")
	if len(state.Members) != 3 || state.Members[1].ID != "a" || state.Members[2].ID != "b" || state.Members[1].DisplayName != "Same name" || !state.Members[1].Ready || state.Members[2].Ready {
		t.Fatalf("unexpected membership/readiness: %+v", state)
	}
	if err := r.SetReady("a", false); err != nil {
		t.Fatal(err)
	}
	if lobby(t, r, "a").Members[1].Ready {
		t.Fatal("readiness could not be withdrawn")
	}
}

func TestLobbyErrorsAreAtomic(t *testing.T) {
	r := createRoom(t, deterministicRegistry(), "admin")
	tests := []struct {
		name string
		act  func() error
		want error
	}{
		{"empty identity", func() error { return r.Join("", "Alice") }, ErrInvalidMember},
		{"blank name", func() error { return r.Join("a", " \t") }, ErrInvalidMember},
		{"duplicate identity", func() error { return r.Join("admin", "Other name") }, ErrAlreadyMember},
		{"unknown ready actor", func() error { return r.SetReady("outsider", true) }, ErrNotMember},
		{"unknown departure", func() error { return r.Leave("outsider") }, ErrNotMember},
		{"unauthorized snapshot", func() error { _, err := r.Snapshot("outsider"); return err }, ErrNotMember},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := lobby(t, r, "admin")
			if err := tt.act(); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v; want %v", err, tt.want)
			}
			if !reflect.DeepEqual(before, lobby(t, r, "admin")) {
				t.Fatal("rejected lobby action changed state")
			}
		})
	}
}

func TestStartChoices(t *testing.T) {
	tests := []struct {
		name    string
		options StartOptions
	}{
		{"explicit choices", StartOptions{SeatOrder: []game.PlayerID{"p2", "p0", "p1"}, FirstBidder: "p0"}},
		{"random seats explicit bidder", StartOptions{ShuffleSeats: true, FirstBidder: "p1"}},
		{"explicit seats random bidder", StartOptions{SeatOrder: []game.PlayerID{"p2", "p0", "p1"}}},
		{"random choices", StartOptions{ShuffleSeats: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first, second := readyRoom(t, 3), readyRoom(t, 3)
			for _, r := range []*Room{first, second} {
				if err := r.Start("admin", tt.options); err != nil {
					t.Fatal(err)
				}
			}
			state, engine := lobby(t, first, "admin"), engineState(first)
			if !reflect.DeepEqual(engine, engineState(second)) {
				t.Fatal("independent seeded rooms produced nondeterministic starts")
			}
			if state.Admin != "admin" || state.GameID != 1 || state.GamePhase != game.GamePlaying || slices.Contains(state.Participants, game.PlayerID("admin")) || len(state.Participants) != 3 {
				t.Fatalf("nonparticipating admin did not start correct ready set: %+v", state)
			}
			if !tt.options.ShuffleSeats && !slices.Equal(state.Participants, tt.options.SeatOrder) {
				t.Fatal("explicit seat order not preserved")
			}
			if tt.options.FirstBidder != "" && engine.Starter != tt.options.FirstBidder {
				t.Fatal("explicit first bidder not selected")
			}
			if !slices.Contains(state.Participants, engine.Starter) {
				t.Fatal("random starter was not a participant")
			}
			if err := first.Join("late", "Late spectator"); err != nil {
				t.Fatal(err)
			}
			if after := lobby(t, first, "late"); len(after.Members) != 5 || !slices.Equal(after.Participants, state.Participants) {
				t.Fatal("late spectator changed game seats")
			}
			if !reflect.DeepEqual(engine, engineState(first)) {
				t.Fatal("late spectator join changed game or dealt cards")
			}
			if err := first.SetReady("late", true); !errors.Is(err, ErrGameInProgress) {
				t.Fatal("spectator could enter readiness during active game")
			}
			if err := first.Start("admin", tt.options); !errors.Is(err, ErrGameInProgress) {
				t.Fatal("admin could replace active game")
			}
		})
	}
}

func TestStartRejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name    string
		count   int
		actor   game.PlayerID
		options StartOptions
		want    error
	}{
		{"too few ready", 1, "admin", StartOptions{ShuffleSeats: true}, ErrInvalidStart},
		{"too many ready", 25, "admin", StartOptions{ShuffleSeats: true}, ErrInvalidStart},
		{"unknown actor", 2, "outsider", StartOptions{ShuffleSeats: true}, ErrNotMember},
		{"nonadmin", 2, "p0", StartOptions{ShuffleSeats: true}, ErrNotAdmin},
		{"missing order", 2, "admin", StartOptions{}, ErrInvalidStart},
		{"incomplete order", 2, "admin", StartOptions{SeatOrder: []game.PlayerID{"p0"}}, ErrInvalidStart},
		{"duplicate seat", 2, "admin", StartOptions{SeatOrder: []game.PlayerID{"p0", "p0"}}, ErrInvalidStart},
		{"unknown seat", 2, "admin", StartOptions{SeatOrder: []game.PlayerID{"p0", "outsider"}}, ErrInvalidStart},
		{"unready seat", 2, "admin", StartOptions{SeatOrder: []game.PlayerID{"p0", "admin"}}, ErrInvalidStart},
		{"both seat modes", 2, "admin", StartOptions{SeatOrder: []game.PlayerID{"p0", "p1"}, ShuffleSeats: true}, ErrInvalidStart},
		{"unknown starter", 2, "admin", StartOptions{ShuffleSeats: true, FirstBidder: "outsider"}, ErrInvalidStart},
		{"unready admin as starter", 2, "admin", StartOptions{ShuffleSeats: true, FirstBidder: "admin"}, ErrInvalidStart},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := readyRoom(t, tt.count)
			before := lobby(t, r, "admin")
			if err := r.Start(tt.actor, tt.options); !errors.Is(err, tt.want) {
				t.Fatalf("Start error = %v; want %v", err, tt.want)
			}
			if !reflect.DeepEqual(before, lobby(t, r, "admin")) || r.game != nil {
				t.Fatal("invalid start changed lobby or installed a game")
			}
			if r.rng.Int63() != rand.New(rand.NewSource(42)).Int63() {
				t.Fatal("invalid start consumed room randomness")
			}
		})
	}
}

func TestGameCapacityDoesNotLimitSpectators(t *testing.T) {
	r := readyRoom(t, 24)
	for i := 0; i < 10; i++ {
		if err := r.Join(game.PlayerID(fmt.Sprintf("spectator%d", i)), "Viewer"); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Start("admin", StartOptions{ShuffleSeats: true}); err != nil {
		t.Fatal(err)
	}
	state := lobby(t, r, "admin")
	if len(state.Members) != 35 || len(state.Participants) != 24 || len(engineState(r).Undealt) != 0 {
		t.Fatalf("incorrect room/game capacity: %+v", state)
	}
}

func TestLeaveCompletesGameAndAllowsReplay(t *testing.T) {
	r := readyRoom(t, 2)
	if err := r.Join("viewer", "Viewer"); err != nil {
		t.Fatal(err)
	}
	if err := r.Start("admin", StartOptions{SeatOrder: []game.PlayerID{"p0", "p1"}, FirstBidder: "p0"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Leave("p0"); err != nil {
		t.Fatal(err)
	}
	state := lobby(t, r, "admin")
	if state.GamePhase != game.GameFinished || engineState(r).Winner != "p1" {
		t.Fatal("participant leave did not finish game")
	}
	for _, member := range state.Members {
		if member.Ready {
			t.Fatal("game completion did not clear readiness")
		}
	}
	for _, id := range []game.PlayerID{"p1", "viewer"} {
		if err := r.SetReady(id, true); err != nil {
			t.Fatal(err)
		}
	}
	// A spectator leaving the already-finished game must not clear new readiness.
	if err := r.Join("temporary", "Temporary"); err != nil {
		t.Fatal(err)
	}
	if err := r.Leave("temporary"); err != nil {
		t.Fatal(err)
	}
	if err := r.Start("admin", StartOptions{SeatOrder: []game.PlayerID{"viewer", "p1"}, FirstBidder: "viewer"}); err != nil {
		t.Fatal(err)
	}
	state, engine := lobby(t, r, "admin"), engineState(r)
	if state.GameID != 2 || engine.RoundID != 1 || engine.LastResult != nil || engine.Winner != "" || engine.CurrentPlayer != "viewer" || len(engine.Players) != 2 {
		t.Fatalf("replay retained old game state: %+v", engine)
	}
	for _, player := range engine.Players {
		if player.HandSize != 1 || len(player.Hand) != 1 || player.Eliminated || player.Withdrawn {
			t.Fatal("replay retained penalties/elimination")
		}
	}
}

func TestAdminRetainedOnEliminationAndTransferredOnLeave(t *testing.T) {
	r := readyRoom(t, 12)
	if err := r.SetReady("admin", true); err != nil {
		t.Fatal(err)
	}
	if err := r.Start("admin", StartOptions{ShuffleSeats: true, FirstBidder: "admin"}); err != nil {
		t.Fatal(err)
	}
	resolveFalseBid(t, r)
	current := roomView(t, r, "admin")
	challenger := current.Game.LastResult.Challenger
	eliminated := current.Game.LastResult.Loser
	if eliminated != "admin" || lobby(t, r, "admin").Admin != "admin" {
		t.Fatal("admin should be eliminated from game and retain room authority")
	}
	before := engineState(r)
	if err := r.Leave("admin"); err != nil {
		t.Fatal(err)
	}
	state := lobby(t, r, challenger)
	if state.Admin == "admin" || !slices.ContainsFunc(state.Members, func(m Member) bool { return m.ID == state.Admin }) {
		t.Fatal("admin rights did not pass to remaining member")
	}
	if !reflect.DeepEqual(before, engineState(r)) {
		t.Fatal("eliminated member's leave changed active game")
	}
}

func TestLobbySnapshotOwnershipAndRoomIsolation(t *testing.T) {
	registry := deterministicRegistry()
	first, second := createRoom(t, registry, "admin"), createRoom(t, registry, "admin")
	for _, r := range []*Room{first, second} {
		for _, id := range []game.PlayerID{"a", "b"} {
			if err := r.Join(id, string(id)); err != nil {
				t.Fatal(err)
			}
			if err := r.SetReady(id, true); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.Start("admin", StartOptions{ShuffleSeats: true}); err != nil {
			t.Fatal(err)
		}
	}
	if first.rng == second.rng || first.game == second.game || !reflect.DeepEqual(engineState(first), engineState(second)) {
		t.Fatal("rooms share objects or fail independent seeded randomness")
	}
	before := lobby(t, first, "admin")
	snapshot := lobby(t, first, "admin")
	snapshot.Members[0].DisplayName = "changed"
	snapshot.Members[0].Ready = true
	snapshot.Members = append(snapshot.Members, Member{})
	snapshot.Participants[0] = "changed"
	if !reflect.DeepEqual(before, lobby(t, first, "admin")) {
		t.Fatal("lobby snapshot aliases room state")
	}
	other := engineState(second)
	if err := first.Leave("a"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(other, engineState(second)) || len(lobby(t, second, "admin").Members) != 3 {
		t.Fatal("one room changed another's membership, randomness, or game")
	}
}

func TestAdminSuccessionUsesRoomRandomness(t *testing.T) {
	chosen := make(map[game.PlayerID]bool)
	for seed := int64(0); seed < 16; seed++ {
		registry := deterministicRegistry()
		registry.newSeed = func() (int64, error) { return seed, nil }
		r := createRoom(t, registry, "admin")
		for _, id := range []game.PlayerID{"a", "b", "c"} {
			if err := r.Join(id, string(id)); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.Leave("admin"); err != nil {
			t.Fatal(err)
		}
		admin := lobby(t, r, "a").Admin
		if admin != "a" && admin != "b" && admin != "c" {
			t.Fatal("new admin is not a remaining member")
		}
		chosen[admin] = true
	}
	if len(chosen) < 2 {
		t.Fatal("admin succession ignored different fixed random sources")
	}
}

func readyRoom(t *testing.T, count int) *Room {
	t.Helper()
	r := createRoom(t, deterministicRegistry(), "admin")
	for i := 0; i < count; i++ {
		id := game.PlayerID(fmt.Sprintf("p%d", i))
		if err := r.Join(id, string(id)); err != nil {
			t.Fatal(err)
		}
		if err := r.SetReady(id, true); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func engineState(r *Room) game.GameState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.game.State()
}
