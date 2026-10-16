package game

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

// Until challenge resolution/progression create these states through public
// commands, fixtures exercise the guards without exposing mutation setters.
func TestGameInactiveAndPhaseGuards(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Game)
		want  error
	}{
		{"eliminated actor", func(g *Game) { g.players[0].Eliminated = true }, ErrInactivePlayer},
		{"withdrawn actor", func(g *Game) { g.players[0].Withdrawn = true }, ErrInactivePlayer},
		{"eliminated out of turn", func(g *Game) { g.players[0].Eliminated = true; g.turn = 1 }, ErrInactivePlayer},
		{"round resolution", func(g *Game) { g.roundPhase = RoundResolution }, ErrWrongPhase},
		{"finished game", func(g *Game) { g.phase = GameFinished }, ErrWrongPhase},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, err := NewGame([]PlayerID{"a", "b", "c"}, "a", rand.New(rand.NewSource(1)))
			if err != nil {
				t.Fatal(err)
			}
			tt.setup(g)
			before := g.State()
			if err := g.PlaceBid("a", Bid{Category: Pair, PrimaryRank: Nine}); !errors.Is(err, tt.want) {
				t.Errorf("bid error = %v; want %v", err, tt.want)
			}
			if err := g.ValidateChallenge("a"); !errors.Is(err, tt.want) {
				t.Errorf("challenge eligibility error = %v; want %v", err, tt.want)
			}
			if err := g.Challenge("a"); !errors.Is(err, tt.want) {
				t.Errorf("challenge error = %v; want %v", err, tt.want)
			}
			if !reflect.DeepEqual(before, g.State()) {
				t.Error("rejected action changed state")
			}
		})
	}
}

func TestGameTurnSkipsEliminatedSeats(t *testing.T) {
	tests := []struct {
		name       string
		starter    PlayerID
		eliminated []int
		want       PlayerID
	}{
		{"adjacent seat", "a", []int{1}, "c"},
		{"several seats", "a", []int{1, 2}, "d"},
		{"wraparound", "d", []int{0, 1}, "c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, err := NewGame([]PlayerID{"a", "b", "c", "d"}, tt.starter, rand.New(rand.NewSource(1)))
			if err != nil {
				t.Fatal(err)
			}
			for _, seat := range tt.eliminated {
				g.players[seat].Eliminated = true
			}
			if err := g.PlaceBid(tt.starter, Bid{Category: HighCard, PrimaryRank: Nine}); err != nil {
				t.Fatal(err)
			}
			if got := g.State().CurrentPlayer; got != tt.want {
				t.Errorf("next turn = %q; want %q", got, tt.want)
			}
		})
	}
}
