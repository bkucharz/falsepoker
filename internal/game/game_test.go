package game_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"testing"

	"falsepoker/internal/game"
)

func TestNewGame(t *testing.T) {
	tests := []struct {
		first, last int
		limit       int
	}{
		{2, 4, 6},
		{5, 6, 5},
		{7, 8, 4},
		{9, 12, 3},
		{13, 24, 2},
	}
	for _, tt := range tests {
		for count := tt.first; count <= tt.last; count++ {
			t.Run(fmt.Sprintf("%d participants", count), func(t *testing.T) {
				seats := participants(count)
				starter := seats[count-1]
				g, err := game.NewGame(seats, starter, rand.New(rand.NewSource(42)))
				if err != nil {
					t.Fatal(err)
				}
				state := g.State()
				if state.Phase != game.GamePlaying || state.RoundPhase != game.RoundBidding || state.RoundID != 1 {
					t.Fatalf("unexpected initial phases/round: %+v", state)
				}
				if state.Starter != starter || state.CurrentPlayer != starter || state.LastBid != nil {
					t.Fatalf("unexpected initial starter/turn/bid: %+v", state)
				}
				if state.EliminationLimit != tt.limit {
					t.Errorf("elimination limit = %d; want %d", state.EliminationLimit, tt.limit)
				}
				if len(state.Players) != count || len(state.Undealt) != 24-count {
					t.Fatalf("participants/undealt = %d/%d; want %d/%d", len(state.Players), len(state.Undealt), count, 24-count)
				}
				cards := slices.Clone(state.Undealt)
				for seat, player := range state.Players {
					if player.ID != seats[seat] || player.HandSize != 1 || len(player.Hand) != 1 || player.Eliminated {
						t.Errorf("invalid initial participant at seat %d: %+v", seat, player)
					}
					cards = append(cards, player.Hand...)
				}
				assertSameCards(t, game.NewDeck().Cards(), cards)
			})
		}
	}
}

func TestNewGameInvalid(t *testing.T) {
	tests := []struct {
		name    string
		seats   []game.PlayerID
		starter game.PlayerID
		nilRNG  bool
		want    error
	}{
		{"no participants", nil, "a", false, game.ErrInvalidGame},
		{"one participant", []game.PlayerID{"a"}, "a", false, game.ErrInvalidGame},
		{"too many participants", participants(25), "player-0", false, game.ErrInvalidGame},
		{"empty identity", []game.PlayerID{"a", ""}, "a", false, game.ErrInvalidGame},
		{"duplicate identity", []game.PlayerID{"a", "a"}, "a", false, game.ErrInvalidGame},
		{"unknown starter", []game.PlayerID{"a", "b"}, "c", false, game.ErrInvalidGame},
		{"missing starter", []game.PlayerID{"a", "b"}, "", false, game.ErrInvalidGame},
		{"nil randomness", []game.PlayerID{"a", "b"}, "a", true, game.ErrInvalidRandomSource},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(7))
			control := rand.New(rand.NewSource(7))
			if tt.nilRNG {
				rng = nil
			}
			before := slices.Clone(tt.seats)
			g, err := game.NewGame(tt.seats, tt.starter, rng)
			if g != nil || !errors.Is(err, tt.want) {
				t.Fatalf("NewGame = (%v, %v); want (nil, %v)", g, err, tt.want)
			}
			if !slices.Equal(tt.seats, before) {
				t.Error("invalid configuration mutated seat order")
			}
			if rng != nil && rng.Int63() != control.Int63() {
				t.Error("invalid configuration consumed randomness")
			}
		})
	}
}

func TestNewGameDeterministic(t *testing.T) {
	seats := []game.PlayerID{"b", "a", "c"}
	first := newTestGame(t, seats, "a")
	second := newTestGame(t, seats, "a")
	if !reflect.DeepEqual(first.State(), second.State()) {
		t.Error("identical seeds/configuration produced different initial states")
	}
	before := first.State()
	seats[0] = "changed"
	if !reflect.DeepEqual(before, first.State()) {
		t.Error("mutating supplied seat order changed game state")
	}
}

func TestPlaceBidTurnOrder(t *testing.T) {
	g := newTestGame(t, []game.PlayerID{"b", "a", "c"}, "a")
	initial := g.State()
	tests := []struct {
		actor game.PlayerID
		bid   game.Bid
		next  game.PlayerID
	}{
		{"a", rankClaim(game.Pair, game.Nine, 0), "c"},
		{"c", rankClaim(game.Pair, game.Ten, 0), "b"},
		{"b", rankClaim(game.Pair, game.Ace, 0), "a"},
		{"a", rankClaim(game.TwoPairs, game.Ten, game.Nine), "c"},
	}
	for _, tt := range tests {
		t.Run(string(tt.actor)+" bids", func(t *testing.T) {
			if err := g.PlaceBid(tt.actor, tt.bid); err != nil {
				t.Fatal(err)
			}
			state := g.State()
			if state.CurrentPlayer != tt.next || state.LastBid == nil || *state.LastBid != (game.PlacedBid{PlayerID: tt.actor, Bid: tt.bid}) {
				t.Fatalf("unexpected turn or last bid: %+v", state)
			}
			if state.RoundID != initial.RoundID || state.Starter != initial.Starter || state.EliminationLimit != initial.EliminationLimit || state.Phase != initial.Phase || state.RoundPhase != initial.RoundPhase {
				t.Error("bidding changed round configuration")
			}
			if !reflect.DeepEqual(state.Players, initial.Players) || !slices.Equal(state.Undealt, initial.Undealt) {
				t.Error("bidding changed players or dealt cards")
			}
		})
	}
}

func TestPlaceBidRejectsInvalidActions(t *testing.T) {
	opening := rankClaim(game.Pair, game.King, 0)
	tests := []struct {
		name    string
		opening *game.Bid
		actor   game.PlayerID
		bid     game.Bid
		want    error
	}{
		{"unknown player", nil, "outsider", opening, game.ErrUnknownPlayer},
		{"out of turn opening", nil, "b", opening, game.ErrOutOfTurn},
		{"invalid opening", nil, "a", game.Bid{}, game.ErrInvalidBid},
		{"out of turn raise", &opening, "a", rankClaim(game.Pair, game.Ace, 0), game.ErrOutOfTurn},
		{"invalid raise", &opening, "b", game.Bid{Category: game.Flush}, game.ErrInvalidBid},
		{"equal bid", &opening, "b", opening, game.ErrBidNotHigher},
		{"lower rank", &opening, "b", rankClaim(game.Pair, game.Queen, 0), game.ErrBidNotHigher},
		{"lower category", &opening, "b", rankClaim(game.HighCard, game.Ace, 0), game.ErrBidNotHigher},
		{"suit-only change", &game.Bid{Category: game.Flush, Suit: game.Hearts}, "b", game.Bid{Category: game.Flush, Suit: game.Spades}, game.ErrBidNotHigher},
		{"no raise over maximum", &game.Bid{Category: game.RoyalStraightFlush, Suit: game.Hearts}, "b", game.Bid{Category: game.RoyalStraightFlush, Suit: game.Clubs}, game.ErrBidNotHigher},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newTestGame(t, []game.PlayerID{"a", "b", "c"}, "a")
			if tt.opening != nil {
				if err := g.PlaceBid("a", *tt.opening); err != nil {
					t.Fatal(err)
				}
			}
			before := g.State()
			if err := g.PlaceBid(tt.actor, tt.bid); !errors.Is(err, tt.want) {
				t.Errorf("PlaceBid error = %v; want %v", err, tt.want)
			}
			if !reflect.DeepEqual(before, g.State()) {
				t.Error("rejected action mutated state")
			}
		})
	}
}

func TestImpossibleClaimCanBeBid(t *testing.T) {
	g := newTestGame(t, []game.PlayerID{"a", "b"}, "a")
	// With two dealt cards, a royal straight flush cannot exist, but bluffing is legal.
	if err := g.PlaceBid("a", game.Bid{Category: game.RoyalStraightFlush, Suit: game.Clubs}); err != nil {
		t.Fatalf("valid bluff rejected: %v", err)
	}
	if err := g.ValidateChallenge("b"); err != nil {
		t.Fatalf("maximum bid must remain challengeable: %v", err)
	}
}

func TestValidateChallenge(t *testing.T) {
	tests := []struct {
		name    string
		opening bool
		actor   game.PlayerID
		want    error
	}{
		{"before opening", false, "a", game.ErrNoBid},
		{"unknown player", false, "outsider", game.ErrUnknownPlayer},
		{"out of turn before opening", false, "b", game.ErrOutOfTurn},
		{"eligible challenge", true, "b", nil},
		{"out of turn after opening", true, "a", game.ErrOutOfTurn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newTestGame(t, []game.PlayerID{"a", "b"}, "a")
			if tt.opening {
				if err := g.PlaceBid("a", rankClaim(game.Pair, game.Nine, 0)); err != nil {
					t.Fatal(err)
				}
			}
			before := g.State()
			if err := g.ValidateChallenge(tt.actor); !errors.Is(err, tt.want) {
				t.Errorf("ValidateChallenge error = %v; want %v", err, tt.want)
			}
			if !reflect.DeepEqual(before, g.State()) {
				t.Error("challenge eligibility check mutated state")
			}
		})
	}
}

func TestGameStateOwnership(t *testing.T) {
	g := newTestGame(t, []game.PlayerID{"a", "b", "c"}, "a")
	if err := g.PlaceBid("a", rankClaim(game.Pair, game.Nine, 0)); err != nil {
		t.Fatal(err)
	}
	before := g.State()
	state := g.State()
	state.Players[0].ID = "changed"
	state.Players[0].HandSize = 99
	state.Players[0].Eliminated = true
	state.Players[0].Hand[0] = game.Card{}
	state.Players[0].Hand = append(state.Players[0].Hand, make([]game.Card, 24)...)
	state.Players = append(state.Players, game.PlayerState{})
	state.Undealt[0] = game.Card{}
	state.Undealt = append(state.Undealt, game.Card{})
	state.LastBid.PlayerID = "changed"
	state.LastBid.Bid = game.Bid{}
	if !reflect.DeepEqual(before, g.State()) {
		t.Fatal("snapshot mutation changed authoritative state")
	}
}

func TestZeroGame(t *testing.T) {
	var g game.Game
	state := g.State()
	if state.Phase != game.GameUninitialized || state.RoundPhase != game.RoundUninitialized || state.RoundID != 0 || len(state.Players) != 0 {
		t.Fatalf("unexpected zero game state: %+v", state)
	}
	if err := g.PlaceBid("a", rankClaim(game.Pair, game.Nine, 0)); !errors.Is(err, game.ErrWrongPhase) {
		t.Fatalf("zero game bid error = %v; want ErrWrongPhase", err)
	}
	if err := g.ValidateChallenge("a"); !errors.Is(err, game.ErrWrongPhase) {
		t.Fatalf("zero game challenge error = %v; want ErrWrongPhase", err)
	}
	if err := g.Challenge("a"); !errors.Is(err, game.ErrWrongPhase) {
		t.Fatalf("zero game resolution error = %v; want ErrWrongPhase", err)
	}
}

func participants(count int) []game.PlayerID {
	ids := make([]game.PlayerID, count)
	for i := range ids {
		ids[i] = game.PlayerID(fmt.Sprintf("player-%d", i))
	}
	return ids
}

func newTestGame(t *testing.T, seats []game.PlayerID, starter game.PlayerID) *game.Game {
	t.Helper()
	g, err := game.NewGame(seats, starter, rand.New(rand.NewSource(42)))
	if err != nil {
		t.Fatal(err)
	}
	return g
}
