package transport

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/bkucharz/falsepoker/internal/game"
	"github.com/bkucharz/falsepoker/internal/room"
)

func (f *fixture) state(t *testing.T, client *http.Client, code string) roomState {
	t.Helper()
	result := f.request(t, client, "GET", "/api/v1/rooms/"+code, "", "", nil)
	if result.status != 200 {
		t.Fatalf("snapshot: %d %s", result.status, result.body)
	}
	for _, field := range []string{"Undealt", "undealt", "credential", "rng", "PlayerID"} {
		if strings.Contains(result.body, field) {
			t.Fatalf("private/internal field %s in wire state", field)
		}
	}
	var data struct {
		State roomState `json:"state"`
	}
	if err := json.Unmarshal([]byte(result.body), &data); err != nil {
		t.Fatal(err)
	}
	return data.State
}

func TestHTTPGameSnapshotsRespectPrivacyAndRetainNames(t *testing.T) {
	f := newFixture(t, Config{}, false)
	alice, aliceID := f.actor(t)
	bob, bobID := f.actor(t)
	spectator, _ := f.actor(t)
	created := f.create(t, alice, "Alice")
	path := "/api/v1/rooms/" + created.RoomCode
	if joined := f.request(t, bob, "POST", path+"/join", `{"display_name":"Bob"}`, f.key(), nil); joined.status != 201 {
		t.Fatal(joined.body)
	}
	r, err := f.app.registry.Lookup(created.RoomCode)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{aliceID, bobID} {
		if err := r.SetReady(game.PlayerID(id), true); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Start(game.PlayerID(aliceID), room.StartOptions{SeatOrder: []game.PlayerID{game.PlayerID(aliceID), game.PlayerID(bobID)}, FirstBidder: game.PlayerID(aliceID)}); err != nil {
		t.Fatal(err)
	}
	if joined := f.request(t, spectator, "POST", path+"/join", `{"display_name":"Viewer"}`, f.key(), nil); joined.status != 201 {
		t.Fatal(joined.body)
	}
	a := f.state(t, alice, created.RoomCode)
	b := f.state(t, bob, created.RoomCode)
	v := f.state(t, spectator, created.RoomCode)
	if a.Role != "participant" || b.Role != "participant" || v.Role != "spectator" || a.Game.Phase != "playing" || a.Game.RoundPhase != "bidding" || a.Game.RoundID != "1" || a.GameID != "1" {
		t.Fatal("wrong initial wire roles/phases/IDs")
	}
	if a.Game.CurrentPlayerID == nil || *a.Game.CurrentPlayerID != aliceID || a.Game.NextStarterID != nil || a.Game.WinnerID != nil || a.Game.LastBid != nil || a.Game.LastResult != nil {
		t.Fatal("wrong initial null/turn encoding")
	}
	if len(a.Game.Players[0].Hand) != 1 || a.Game.Players[1].Hand != nil || b.Game.Players[0].Hand != nil || len(b.Game.Players[1].Hand) != 1 || len(v.Game.Players[0].Hand) != 1 || len(v.Game.Players[1].Hand) != 1 {
		t.Fatal("wire payload leaked or omitted hands")
	}
	context := room.GameContext{GameID: 1, RoundID: 1}
	if err := r.PlaceBid(game.PlayerID(aliceID), context, game.Bid{Category: game.RoyalStraightFlush, Suit: game.Clubs}); err != nil {
		t.Fatal(err)
	}
	if err := r.Challenge(game.PlayerID(bobID), context); err != nil {
		t.Fatal(err)
	}
	resolved := f.state(t, bob, created.RoomCode)
	if resolved.Game.RoundPhase != "resolution" || resolved.Game.CurrentPlayerID != nil || resolved.Game.NextStarterID == nil || *resolved.Game.NextStarterID != aliceID || resolved.Game.Players[0].RequiredHandSize != 2 || resolved.Game.Players[0].HandCount != 1 || len(resolved.Game.Players[0].Hand) != 1 {
		t.Fatal("resolution snapshot lacks reveal or distinct hand counts")
	}
	result := resolved.Game.LastResult
	if result == nil || result.ClaimSatisfied || result.LoserID != aliceID || result.ChallengerID != bobID || result.ChallengedBid.Bid.Category != "royal_straight_flush" || result.ChallengedBid.Bid.Suit != "clubs" || result.RoundID != "1" || len(result.RevealedHands) != 2 {
		t.Fatal("wire result omitted challenge data")
	}
	if err := r.NextRound(game.PlayerID(aliceID), context); err != nil {
		t.Fatal(err)
	}
	advanced := f.state(t, bob, created.RoomCode)
	if advanced.Game.RoundID != "2" || advanced.Game.LastBid != nil || advanced.Game.Players[0].Hand != nil || advanced.Game.Players[0].HandCount != 2 || advanced.Game.LastResult.RoundID != "1" {
		t.Fatal("new round leaked cards or lost public previous reveal")
	}
	if left := f.request(t, alice, "POST", path+"/leave", `{"room_id":"`+created.RoomID+`"}`, f.key(), nil); left.status != 200 {
		t.Fatal(left.body)
	}
	finished := f.state(t, bob, created.RoomCode)
	if finished.Game.Phase != "finished" || finished.Game.WinnerID == nil || *finished.Game.WinnerID != bobID || finished.Game.CurrentPlayerID != nil || finished.Game.NextStarterID != nil || finished.Game.Players[0].DisplayName != "Alice" || !finished.Game.Players[0].Withdrawn || len(finished.Game.Players[0].Hand) != 2 {
		t.Fatal("departure win lost historical identity or public cards")
	}
	for _, member := range finished.Members {
		if member.Ready {
			t.Fatal("terminal room view retained readiness")
		}
	}
	if joined := f.request(t, alice, "POST", path+"/join", `{"display_name":"Returning Alice"}`, f.key(), nil); joined.status != 201 {
		t.Fatal(joined.body)
	}
	if old := f.state(t, alice, created.RoomCode); old.Game.Players[0].DisplayName != "Alice" {
		t.Fatal("a rejoin renamed the past participant")
	}
	for _, id := range []string{aliceID, bobID} {
		if err := r.SetReady(game.PlayerID(id), true); err != nil {
			t.Fatal(err)
		}
	}
	view, err := r.View(game.PlayerID(bobID))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Start(view.Admin, room.StartOptions{SeatOrder: []game.PlayerID{game.PlayerID(aliceID), game.PlayerID(bobID)}, FirstBidder: game.PlayerID(aliceID)}); err != nil {
		t.Fatal(err)
	}
	replay := f.state(t, alice, created.RoomCode)
	if replay.GameID != "2" || replay.Game.LastResult != nil || replay.Game.WinnerID != nil || replay.Game.Players[0].DisplayName != "Returning Alice" || replay.Game.Players[1].Hand != nil || len(replay.Game.Players[0].Hand) != 1 {
		t.Fatal("replay leaked a hand or retained old names/result")
	}
}

func TestWireBidEnumsAndOmittedParameters(t *testing.T) {
	tests := []struct {
		bid  game.Bid
		want string
	}{
		{game.Bid{Category: game.HighCard, PrimaryRank: game.Ace}, `{"category":"high_card","primary_rank":"A"}`},
		{game.Bid{Category: game.Pair, PrimaryRank: game.King}, `{"category":"pair","primary_rank":"K"}`},
		{game.Bid{Category: game.TwoPairs, PrimaryRank: game.Queen, SecondaryRank: game.Jack}, `{"category":"two_pairs","primary_rank":"Q","secondary_rank":"J"}`},
		{game.Bid{Category: game.LowStraight}, `{"category":"low_straight"}`},
		{game.Bid{Category: game.HighStraight}, `{"category":"high_straight"}`},
		{game.Bid{Category: game.ThreeOfAKind, PrimaryRank: game.Ten}, `{"category":"three_of_a_kind","primary_rank":"10"}`},
		{game.Bid{Category: game.FullHouse, PrimaryRank: game.Ace, SecondaryRank: game.Nine}, `{"category":"full_house","primary_rank":"A","secondary_rank":"9"}`},
		{game.Bid{Category: game.Flush, Suit: game.Diamonds}, `{"category":"flush","suit":"diamonds"}`},
		{game.Bid{Category: game.FourOfAKind, PrimaryRank: game.Jack}, `{"category":"four_of_a_kind","primary_rank":"J"}`},
		{game.Bid{Category: game.StraightFlush, Suit: game.Hearts}, `{"category":"straight_flush","suit":"hearts"}`},
		{game.Bid{Category: game.RoyalStraightFlush, Suit: game.Spades}, `{"category":"royal_straight_flush","suit":"spades"}`},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if err := tt.bid.Validate(); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(convertPlacedBid(game.PlacedBid{PlayerID: "p", Bid: tt.bid}).Bid)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != tt.want {
				t.Fatalf("bid = %s; want %s", encoded, tt.want)
			}
		})
	}
	if value := counter(^uint64(0)); value != "18446744073709551615" {
		t.Fatal("counter lost uint64 precision")
	}
	view := room.RoomView{Game: &room.GameView{Phase: game.GamePlaying, RoundPhase: game.RoundBidding, Players: []room.PlayerView{{ID: "inactive", Eliminated: true}}}}
	encoded, err := json.Marshal(toRoomState(view))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"hand":[]`) || !strings.Contains(string(encoded), `"members":[]`) {
		t.Fatal("empty arrays encoded as null")
	}
}

func TestHTTPCapturedJoinResultSurvivesConcurrentOwnerLeave(t *testing.T) {
	f := newFixture(t, Config{}, false)
	for iteration := 0; iteration < 24; iteration++ {
		alice, _ := f.actor(t)
		bob, _ := f.actor(t)
		created := f.create(t, alice, "Alice")
		path := "/api/v1/rooms/" + created.RoomCode
		var results [2]received
		for _, err := range workers(2, func(i int) error {
			var err error
			if i == 0 {
				results[i], err = f.do(bob, "POST", path+"/join", `{"display_name":"Bob"}`, f.key(), nil)
			} else {
				results[i], err = f.do(alice, "POST", path+"/leave", `{"room_id":"`+created.RoomID+`"}`, f.key(), nil)
			}
			return err
		}) {
			t.Fatal(err)
		}
		if results[1].status != 200 {
			t.Fatal(results[1].body)
		}
		if results[0].status == 201 {
			var joined membershipResponse
			if err := json.Unmarshal([]byte(results[0].body), &joined); err != nil {
				t.Fatal(err)
			}
			if joined.Revision != "2" || joined.RoomID != created.RoomID {
				t.Fatal("join result was assembled after another mutation")
			}
			state := f.state(t, bob, created.RoomCode)
			if state.Revision != "3" || len(state.Members) != 1 || state.AdminID != joined.PlayerID {
				t.Fatal("join-before-leave lost membership or admin")
			}
			if left := f.request(t, bob, "POST", path+"/leave", `{"room_id":"`+created.RoomID+`"}`, f.key(), nil); left.status != 200 {
				t.Fatal(left.body)
			}
		} else if results[0].status != 404 {
			t.Fatalf("unexpected racing join: %d %s", results[0].status, results[0].body)
		}
	}
}
