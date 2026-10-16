package transport

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bkucharz/falsepoker/internal/game"
)

func TestParseCommandStrictShape(t *testing.T) {
	base := string(commandBody(strings.Repeat("a", 32), roomState{RoomID: "r_example", Revision: "1", GameID: "0"}, "ready", map[string]any{"ready": true}))
	modify := func(field string, value any) string {
		var data map[string]any
		json.Unmarshal([]byte(base), &data)
		if value == nil {
			delete(data, field)
		} else {
			data[field] = value
		}
		result, _ := json.Marshal(data)
		return string(result)
	}
	tests := []struct{ name, body, code string }{
		{"missing field", modify("round_id", nil), "INVALID_REQUEST"},
		{"extra actor", modify("player_id", "victim"), "INVALID_REQUEST"},
		{"case alias", strings.Replace(base, `"action"`, `"Action"`, 1), "INVALID_REQUEST"},
		{"request ID uppercase", modify("request_id", strings.Repeat("A", 32)), "INVALID_REQUEST"},
		{"request ID wrong size", modify("request_id", "a"), "INVALID_REQUEST"},
		{"numeric counter", modify("expected_revision", 1), "INVALID_REQUEST"},
		{"counter leading zero", modify("game_id", "00"), "INVALID_REQUEST"},
		{"counter sign", modify("game_id", "+0"), "INVALID_REQUEST"},
		{"counter overflow", modify("game_id", "18446744073709551616"), "INVALID_REQUEST"},
		{"counter whitespace", modify("round_id", " 0"), "INVALID_REQUEST"},
		{"wrong type", modify("type", "snapshot"), "INVALID_REQUEST"},
		{"null payload", modify("data", json.RawMessage("null")), "INVALID_REQUEST"},
		{"null ready", modify("data", map[string]any{"ready": nil}), "INVALID_REQUEST"},
		{"numeric ready", modify("data", map[string]any{"ready": 1}), "INVALID_REQUEST"},
		{"extra payload", modify("data", map[string]any{"ready": true, "seed": 0}), "INVALID_REQUEST"},
		{"duplicate nested", strings.Replace(base, `"ready":true`, `"ready":true,"ready":false`, 1), "INVALID_REQUEST"},
		{"escaped duplicate", strings.Replace(base, `"ready":true`, `"ready":true,"\u0072eady":false`, 1), "INVALID_REQUEST"},
		{"trailing JSON", base + `{}`, "INVALID_REQUEST"},
		{"array", `[]`, "INVALID_REQUEST"},
		{"unknown action", modify("action", "leave"), "UNKNOWN_COMMAND"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, f := parseCommand([]byte(tt.body))
			if f == nil || f.code != tt.code {
				t.Fatalf("failure=%+v; want %s", f, tt.code)
			}
		})
	}
	var reordered map[string]any
	json.Unmarshal([]byte(base), &reordered)
	pretty, _ := json.MarshalIndent(reordered, "", "  ")
	one, f := parseCommand([]byte(base))
	if f != nil {
		t.Fatal(f)
	}
	two, f := parseCommand(pretty)
	if f != nil || one.Fingerprint != two.Fingerprint {
		t.Fatal("formatting changed intent")
	}
}

func TestParseBids(t *testing.T) {
	valid := []game.Bid{
		{Category: game.HighCard, PrimaryRank: game.Nine},
		{Category: game.Pair, PrimaryRank: game.Ace},
		{Category: game.TwoPairs, PrimaryRank: game.King, SecondaryRank: game.Queen},
		{Category: game.LowStraight}, {Category: game.HighStraight},
		{Category: game.ThreeOfAKind, PrimaryRank: game.Jack},
		{Category: game.FullHouse, PrimaryRank: game.Ten, SecondaryRank: game.Ace},
		{Category: game.Flush, Suit: game.Clubs},
		{Category: game.FourOfAKind, PrimaryRank: game.Queen},
		{Category: game.StraightFlush, Suit: game.Hearts},
		{Category: game.RoyalStraightFlush, Suit: game.Spades},
	}
	for _, bid := range valid {
		wire := convertPlacedBid(game.PlacedBid{Bid: bid}).Bid
		data, _ := json.Marshal(wire)
		t.Run(wire.Category, func(t *testing.T) {
			got, f := parseBid(data)
			if f != nil || got != bid {
				t.Fatalf("bid=%+v failure=%+v", got, f)
			}
		})
	}
	for _, data := range []string{
		`null`, `{}`, `{"category":"unknown"}`, `{"category":"pair","primary_rank":"8"}`,
		`{"category":"pair","primary_rank":null}`, `{"category":"pair","primary_rank":"9","suit":null}`,
		`{"category":"low_straight","primary_rank":"A"}`, `{"category":"flush","suit":"unknown"}`,
		`{"category":"two_pairs","primary_rank":"9","secondary_rank":"A"}`,
		`{"category":"full_house","primary_rank":"A","secondary_rank":"A"}`,
	} {
		t.Run(data, func(t *testing.T) {
			if _, f := parseBid([]byte(data)); f == nil {
				t.Fatal("invalid bid accepted")
			}
		})
	}
}

func TestParseStartOptions(t *testing.T) {
	tests := []struct {
		name  string
		data  any
		valid bool
	}{
		{"random", map[string]any{"seat_order": nil, "shuffle_seats": true, "first_bidder": nil}, true},
		{"explicit", map[string]any{"seat_order": []string{"p_a", "p_b"}, "shuffle_seats": false, "first_bidder": "p_b"}, true},
		{"random starter", map[string]any{"seat_order": []string{"p_a", "p_b"}, "shuffle_seats": false, "first_bidder": nil}, true},
		{"explicit starter shuffled", map[string]any{"seat_order": nil, "shuffle_seats": true, "first_bidder": "p_a"}, true},
		{"missing starter", map[string]any{"seat_order": nil, "shuffle_seats": true}, false},
		{"both order choices", map[string]any{"seat_order": []string{"p_a", "p_b"}, "shuffle_seats": true, "first_bidder": nil}, false},
		{"no order", map[string]any{"seat_order": nil, "shuffle_seats": false, "first_bidder": nil}, false},
		{"null shuffle", map[string]any{"seat_order": nil, "shuffle_seats": nil, "first_bidder": nil}, false},
		{"null seat", map[string]any{"seat_order": []any{"p_a", nil}, "shuffle_seats": false, "first_bidder": nil}, false},
		{"empty starter", map[string]any{"seat_order": nil, "shuffle_seats": true, "first_bidder": ""}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := commandBody(strings.Repeat("a", 32), roomState{RoomID: "r_room", Revision: "1", GameID: "0"}, "start", tt.data)
			_, f := parseCommand(body)
			if (f == nil) != tt.valid {
				t.Fatalf("failure=%+v valid=%t", f, tt.valid)
			}
		})
	}
}
