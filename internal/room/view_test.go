package room

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bkucharz/falsepoker/internal/game"
)

func TestViewLobbyAndAccess(t *testing.T) {
	r := readyRoom(t, 2)
	before := lobby(t, r, "admin")
	view := roomView(t, r, "admin")
	if view.Code != before.Code || view.Admin != before.Admin || view.GameID != 0 || view.Recipient != "admin" || view.Role != Spectator || view.Game != nil || !reflect.DeepEqual(view.Members, before.Members) {
		t.Fatalf("unexpected lobby view: %+v", view)
	}
	for _, actor := range []game.PlayerID{"", "outsider"} {
		got, err := r.View(actor)
		if !errors.Is(err, ErrNotMember) || !reflect.DeepEqual(got, RoomView{}) {
			t.Fatalf("View(%q) = %+v, %v; want empty view and ErrNotMember", actor, got, err)
		}
	}
	if err := r.Leave("p0"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.View("p0"); !errors.Is(err, ErrNotMember) {
		t.Fatalf("departed member view error = %v", err)
	}
	for _, id := range []game.PlayerID{"p1", "admin"} {
		if err := r.Leave(id); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := r.View("admin"); !errors.Is(err, ErrRoomClosed) || !reflect.DeepEqual(got, RoomView{}) {
		t.Fatalf("closed room view = %+v, %v", got, err)
	}
}

func TestViewGamePhases(t *testing.T) {
	tests := []struct {
		name       string
		advance    func(*testing.T, *Room)
		phase      game.GamePhase
		roundPhase game.RoundPhase
		round      uint64
		result     bool
		revealAll  bool
	}{
		{"opening", func(*testing.T, *Room) {}, game.GamePlaying, game.RoundBidding, 1, false, false},
		{"bid placed", func(t *testing.T, r *Room) {
			if err := r.PlaceBid("p0", gameContext(roomView(t, r, "p0")), game.Bid{Category: game.HighCard, PrimaryRank: game.Ace}); err != nil {
				t.Fatal(err)
			}
		}, game.GamePlaying, game.RoundBidding, 1, false, false},
		{"resolution", resolveFalseBid, game.GamePlaying, game.RoundResolution, 1, true, true},
		{"withdrawn bidder reveal", func(t *testing.T, r *Room) {
			if err := r.PlaceBid("p0", gameContext(roomView(t, r, "p0")), game.Bid{Category: game.RoyalStraightFlush, Suit: game.Clubs}); err != nil {
				t.Fatal(err)
			}
			if err := r.Leave("p0"); err != nil {
				t.Fatal(err)
			}
			if err := r.Challenge("p1", gameContext(roomView(t, r, "p1"))); err != nil {
				t.Fatal(err)
			}
		}, game.GamePlaying, game.RoundResolution, 1, true, true},
		{"next round", func(t *testing.T, r *Room) {
			resolveFalseBid(t, r)
			advanceRound(t, r)
		}, game.GamePlaying, game.RoundBidding, 2, true, false},
		{"departure win without challenge", func(t *testing.T, r *Room) {
			for _, id := range []game.PlayerID{"p0", "p1"} {
				if err := r.Leave(id); err != nil {
					t.Fatal(err)
				}
			}
		}, game.GameFinished, game.RoundResolution, 1, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := startedViewRoom(t, 3)
			tt.advance(t, r)
			state := engineState(r)
			if state.Phase != tt.phase || state.RoundPhase != tt.roundPhase || state.RoundID != tt.round || (state.LastResult != nil) != tt.result {
				t.Fatalf("incorrect fixture phase: %+v", state)
			}
			for _, member := range lobby(t, r, "admin").Members {
				view := roomView(t, r, member.ID)
				assertPublicGame(t, view, state)
				wantRole := Participant
				if member.ID == "admin" {
					wantRole = Spectator
				}
				if view.Role != wantRole {
					t.Fatalf("%s role = %v; want %v", member.ID, view.Role, wantRole)
				}
				for seat, player := range view.Game.Players {
					visible := tt.revealAll || member.ID == "admin" || player.ID == member.ID
					assertVisibleHand(t, player, state.Players[seat].Hand, visible)
				}
			}
			if tt.name == "resolution" {
				player := roomView(t, r, "admin").Game.Players[0]
				if player.HandCount != 1 || player.RequiredHandSize != 2 {
					t.Fatal("resolution must distinguish dealt count from next deal size")
				}
			}
			if tt.name == "next round" {
				view := roomView(t, r, "p0")
				if view.Game.CurrentPlayer != "p0" || view.Game.LastBid != nil || view.Game.LastResult.RoundID != 1 || len(view.Game.Players[0].Hand) != 2 {
					t.Fatal("new deal lost prior reveal or retained old turn/bid/hand size")
				}
			}
		})
	}
}

func TestViewSpectatorsAndAdminPrivileges(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *Room)
		actor game.PlayerID
		role  ViewerRole
	}{
		{"nonparticipating admin", func(*testing.T, *Room) {}, "admin", Spectator},
		{"ordinary late spectator", func(t *testing.T, r *Room) {
			if err := r.Join("late", "Late"); err != nil {
				t.Fatal(err)
			}
		}, "late", Spectator},
		{"withdrawn member rejoins as spectator", func(t *testing.T, r *Room) {
			if err := r.Leave("p0"); err != nil {
				t.Fatal(err)
			}
			if err := r.Join("p0", "Returning"); err != nil {
				t.Fatal(err)
			}
		}, "p0", Spectator},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := startedViewRoom(t, 3)
			tt.setup(t, r)
			view, state := roomView(t, r, tt.actor), engineState(r)
			if view.Role != tt.role {
				t.Fatalf("role = %v; want %v", view.Role, tt.role)
			}
			for seat, player := range view.Game.Players {
				visible := (tt.role == Spectator && !player.Withdrawn) || (tt.role == Participant && player.ID == tt.actor)
				assertVisibleHand(t, player, state.Players[seat].Hand, visible)
			}
		})
	}
	t.Run("participating admin", func(t *testing.T) {
		r := readyRoom(t, 2)
		if err := r.SetReady("admin", true); err != nil {
			t.Fatal(err)
		}
		if err := r.Start("admin", StartOptions{SeatOrder: []game.PlayerID{"admin", "p0", "p1"}, FirstBidder: "admin"}); err != nil {
			t.Fatal(err)
		}
		view, state := roomView(t, r, "admin"), engineState(r)
		if view.Role != Participant {
			t.Fatal("participating admin must have participant access")
		}
		for seat, player := range view.Game.Players {
			assertVisibleHand(t, player, state.Players[seat].Hand, player.ID == "admin")
		}
	})
}

func TestViewEliminatedPlayerAndRetainedReveal(t *testing.T) {
	r := startedViewRoom(t, 13)
	resolveFalseBid(t, r)
	resolved := roomView(t, r, "p0")
	if resolved.Role != Spectator || !resolved.Game.Players[0].Eliminated || resolved.Game.LastResult.Loser != "p0" {
		t.Fatal("eliminated player must become a spectator immediately")
	}
	for seat, player := range resolved.Game.Players {
		assertVisibleHand(t, player, engineState(r).Players[seat].Hand, true)
	}
	advanceRound(t, r)
	state := engineState(r)
	for _, actor := range []game.PlayerID{"p0", "p1", "admin"} {
		view := roomView(t, r, actor)
		assertPublicGame(t, view, state)
		if !reflect.DeepEqual(view.Game.LastResult, resolved.Game.LastResult) {
			t.Fatal("new round lost publicly revealed completed-round cards")
		}
		for seat, player := range view.Game.Players {
			visible := !player.Eliminated && (actor != "p1" || player.ID == "p1")
			assertVisibleHand(t, player, state.Players[seat].Hand, visible)
		}
	}
}

func TestViewFinishedChallengeAndReplayAccess(t *testing.T) {
	r := startedViewRoom(t, 2)
	// Repeated false bids by the surviving loser eventually eliminate p0.
	for engineState(r).Phase == game.GamePlaying {
		resolveFalseBid(t, r)
		if engineState(r).Phase == game.GamePlaying {
			advanceRound(t, r)
		}
	}
	finished := engineState(r)
	for _, actor := range []game.PlayerID{"p0", "p1", "admin"} {
		view := roomView(t, r, actor)
		assertPublicGame(t, view, finished)
		if view.Game.Winner != "p1" || view.Game.LastResult == nil || (actor == "p0" && view.Role != Spectator) {
			t.Fatal("finished view omitted winner, final reveal, or eliminated role")
		}
		for seat, player := range view.Game.Players {
			assertVisibleHand(t, player, finished.Players[seat].Hand, true)
		}
	}
	for _, id := range []game.PlayerID{"p0", "admin"} {
		if err := r.SetReady(id, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Start("admin", StartOptions{SeatOrder: []game.PlayerID{"admin", "p0"}, FirstBidder: "admin"}); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		actor game.PlayerID
		role  ViewerRole
		cards int
	}{{"admin", Participant, 1}, {"p0", Participant, 1}, {"p1", Spectator, 2}} {
		view := roomView(t, r, tt.actor)
		if view.Role != tt.role || view.GameID != 2 || view.Game.RoundID != 1 || view.Game.LastResult != nil || view.Game.Winner != "" {
			t.Fatalf("replay retained previous identity, role, or result: %+v", view)
		}
		total := 0
		for _, player := range view.Game.Players {
			total += len(player.Hand)
			if tt.role == Participant && player.ID != tt.actor && player.Hand != nil {
				t.Fatal("former spectator retained access after becoming a participant")
			}
		}
		if total != tt.cards {
			t.Fatalf("%s received %d cards; want %d", tt.actor, total, tt.cards)
		}
	}
}

func TestViewOwnsMutableData(t *testing.T) {
	r := startedViewRoom(t, 3)
	resolveFalseBid(t, r)
	advanceRound(t, r)
	current := roomView(t, r, "admin")
	if err := r.PlaceBid(current.Game.CurrentPlayer, gameContext(current), game.Bid{Category: game.HighCard, PrimaryRank: game.Nine}); err != nil {
		t.Fatal(err)
	}
	before, other := roomView(t, r, "admin"), roomView(t, r, "p1")
	state := engineState(r)
	mutated := roomView(t, r, "admin")
	mutated.Members[0].DisplayName = "changed"
	mutated.Members[1].Ready = false
	mutated.Game.Players[0].ID = "changed"
	mutated.Game.Players[0].Hand[0] = game.Card{}
	mutated.Game.Players = append(mutated.Game.Players, PlayerView{})
	mutated.Game.LastBid.Bid.PrimaryRank = game.Ace
	mutated.Game.LastResult.ChallengedBid.PlayerID = "changed"
	mutated.Game.LastResult.RevealedHands[0].Cards[0] = game.Card{}
	mutated.Game.LastResult.RevealedHands[0].PlayerID = "changed"
	mutated.Game.LastResult.RevealedHands = append(mutated.Game.LastResult.RevealedHands, game.RevealedHand{})
	if !reflect.DeepEqual(before, roomView(t, r, "admin")) || !reflect.DeepEqual(other, roomView(t, r, "p1")) || !reflect.DeepEqual(state, engineState(r)) {
		t.Fatal("mutating one view affected the room, engine, or another recipient")
	}
	before.Game.Players[0].Hand[0] = game.Card{}
	if before.Game.LastResult.RevealedHands[0].Cards[0] == (game.Card{}) {
		t.Fatal("current hand and completed reveal share storage")
	}
}

func TestViewSerializedDataDoesNotExposeEngineInternals(t *testing.T) {
	r := startedViewRoom(t, 3)
	view := roomView(t, r, "p0")
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Undealt", "Credential", "Token", "rng", "registry"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("view serialized private field %q", forbidden)
		}
	}
	// Inspect the serialized card objects too, so a leak under another field
	// name cannot be hidden by checking only forbidden field names.
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	var cards []game.Card
	var collect func(any)
	collect = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			if rank, ok := value["Rank"].(float64); ok {
				cards = append(cards, game.Card{Rank: game.Rank(rank), Suit: game.Suit(value["Suit"].(float64))})
			}
			for _, child := range value {
				collect(child)
			}
		case []any:
			for _, child := range value {
				collect(child)
			}
		}
	}
	collect(decoded)
	if !slices.Equal(cards, engineState(r).Players[0].Hand) {
		t.Fatalf("serialized participant view contains unauthorized cards: %v", cards)
	}
}

func TestConcurrentViewsAndRoomChanges(t *testing.T) {
	r := startedViewRoom(t, 3)
	for _, err := range concurrent(96, func(i int) error {
		if i%3 == 0 {
			id := game.PlayerID(fmt.Sprintf("late%d", i))
			if err := r.Join(id, "Late"); err != nil {
				return err
			}
			return r.Leave(id)
		}
		actor := game.PlayerID("p0")
		if i%3 == 1 {
			actor = "admin"
		}
		view, err := r.View(actor)
		if err != nil {
			return err
		}
		for _, player := range view.Game.Players {
			if actor == "p0" && player.ID != actor && player.Hand != nil {
				return errors.New("concurrent view leaked another hand")
			}
			if player.Hand != nil {
				player.Hand[0] = game.Card{}
			}
		}
		view.Members[0].DisplayName = "changed"
		return nil
	}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, player := range roomView(t, r, "admin").Game.Players {
		if player.Hand[0] == (game.Card{}) {
			t.Fatal("concurrent view mutation changed authoritative hand")
		}
	}
}

func TestConcurrentViewsDuringRoundTransitions(t *testing.T) {
	r := startedViewRoom(t, 2)
	for _, err := range concurrent(64, func(i int) error {
		if i == 0 {
			for round := 0; round < 5; round++ {
				view, err := r.View("p0")
				if err != nil {
					return err
				}
				context := gameContext(view)
				bid, err := falseRoyalBid(engineState(r))
				if err != nil {
					return err
				}
				if err := r.PlaceBid("p0", context, bid); err != nil {
					return err
				}
				if err := r.Challenge("p1", context); err != nil {
					return err
				}
				if round < 4 {
					if err := r.NextRound("p0", context); err != nil {
						return err
					}
				}
			}
			return nil
		}
		for read := 0; read < 20; read++ {
			view, err := r.View("p1")
			if err != nil {
				return err
			}
			g := view.Game
			if g.RoundPhase == game.RoundBidding {
				opening := g.CurrentPlayer == "p0" && g.LastBid == nil
				responding := g.CurrentPlayer == "p1" && g.LastBid != nil && g.LastBid.PlayerID == "p0"
				if (!opening && !responding) || g.Winner != "" || g.Players[0].Hand != nil || len(g.Players[1].Hand) != 1 {
					return errors.New("bidding view mixes phases or exposes opponent's hand")
				}
			} else {
				if g.CurrentPlayer != "" || g.LastResult == nil || g.LastResult.RoundID != g.RoundID || len(g.Players[0].Hand) != int(g.RoundID) {
					return errors.New("resolution view mixes rounds or omits revealed cards")
				}
				if g.Phase == game.GameFinished && (g.Winner != "p1" || g.RoundID != 5) {
					return errors.New("finished view omits winner or final round")
				}
			}
		}
		return nil
	}) {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func roomView(t *testing.T, r *Room, actor game.PlayerID) RoomView {
	t.Helper()
	view, err := r.View(actor)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func startedViewRoom(t *testing.T, count int) *Room {
	t.Helper()
	r := readyRoom(t, count)
	seats := make([]game.PlayerID, count)
	for i := range seats {
		seats[i] = game.PlayerID(fmt.Sprintf("p%d", i))
	}
	if err := r.Start("admin", StartOptions{SeatOrder: seats, FirstBidder: "p0"}); err != nil {
		t.Fatal(err)
	}
	return r
}

func resolveFalseBid(t *testing.T, r *Room) {
	t.Helper()
	view := roomView(t, r, "admin")
	bid, err := falseRoyalBid(engineState(r))
	if err != nil {
		t.Fatal(err)
	}
	context := gameContext(view)
	if err := r.PlaceBid(view.Game.CurrentPlayer, context, bid); err != nil {
		t.Fatal(err)
	}
	if err := r.Challenge(roomView(t, r, "admin").Game.CurrentPlayer, context); err != nil {
		t.Fatal(err)
	}
}

func falseRoyalBid(state game.GameState) (game.Bid, error) {
	var cards []game.Card
	for _, player := range state.Players {
		cards = append(cards, player.Hand...)
	}
	for _, suit := range []game.Suit{game.Clubs, game.Diamonds, game.Hearts, game.Spades} {
		bid := game.Bid{Category: game.RoyalStraightFlush, Suit: suit}
		exists, err := bid.IsSatisfiedBy(cards)
		if err != nil {
			return game.Bid{}, err
		}
		if !exists {
			return bid, nil
		}
	}
	return game.Bid{}, errors.New("fixture has no false royal straight flush")
}

func advanceRound(t *testing.T, r *Room) {
	t.Helper()
	view := roomView(t, r, "admin")
	if err := r.NextRound(view.Game.NextStarter, gameContext(view)); err != nil {
		t.Fatal(err)
	}
}

func assertVisibleHand(t *testing.T, player PlayerView, dealt []game.Card, visible bool) {
	t.Helper()
	if visible {
		if !slices.Equal(player.Hand, dealt) {
			t.Fatalf("%s hand = %v; want %v", player.ID, player.Hand, dealt)
		}
	} else if player.Hand != nil {
		t.Fatalf("%s unauthorized hand = %v", player.ID, player.Hand)
	}
}

func assertPublicGame(t *testing.T, view RoomView, state game.GameState) {
	t.Helper()
	g := view.Game
	if view.GameID != 1 || g == nil || g.Phase != state.Phase || g.RoundPhase != state.RoundPhase || g.RoundID != state.RoundID || g.EliminationLimit != state.EliminationLimit || g.Starter != state.Starter || g.NextStarter != state.NextStarter || g.CurrentPlayer != state.CurrentPlayer || g.Winner != state.Winner || !reflect.DeepEqual(g.LastBid, state.LastBid) || !reflect.DeepEqual(g.LastResult, state.LastResult) {
		t.Fatalf("view lacks public game state: %+v; want %+v", g, state)
	}
	if len(g.Players) != len(state.Players) {
		t.Fatal("view lost fixed game seats")
	}
	for seat, player := range g.Players {
		want := state.Players[seat]
		if player.ID != want.ID || player.HandCount != len(want.Hand) || player.RequiredHandSize != want.HandSize || player.Eliminated != want.Eliminated || player.Withdrawn != want.Withdrawn {
			t.Fatalf("incorrect public player metadata: %+v; want %+v", player, want)
		}
	}
}
