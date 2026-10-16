package transport

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/bkucharz/falsepoker/internal/game"
	"github.com/bkucharz/falsepoker/internal/room"
)

// object enforces exact, case-sensitive field names and object shape. Pointers
// alone cannot distinguish an omitted nullable start option from explicit null.
func object(data []byte, fields ...string) (map[string]json.RawMessage, *failure) {
	var values map[string]json.RawMessage
	if f := decodeJSON(data, &values); f != nil {
		return nil, f
	}
	if len(values) != len(fields) {
		return nil, invalidJSON()
	}
	for _, name := range fields {
		if _, ok := values[name]; !ok {
			return nil, invalidJSON()
		}
	}
	return values, nil
}

func stringValue(data json.RawMessage) (string, bool) {
	var value string
	if len(data) == 0 || string(data) == "null" || json.Unmarshal(data, &value) != nil {
		return "", false
	}
	return value, true
}

func decimalValue(data json.RawMessage) (uint64, bool) {
	s, ok := stringValue(data)
	if !ok || s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	v, err := strconv.ParseUint(s, 10, 64)
	return v, err == nil
}

func parseCommand(data []byte) (room.Command, *failure) {
	var c room.Command
	v, f := object(data, "type", "request_id", "room_id", "expected_revision", "game_id", "round_id", "action", "data")
	if f != nil {
		return c, f
	}
	// Recover a valid request ID for errors only after strict envelope validation.
	id, ok := stringValue(v["request_id"])
	if !ok || !lowerHex(id, 32) {
		return c, invalidJSON()
	}
	c.RequestID = id
	kind, ok := stringValue(v["type"])
	if !ok || kind != "command" {
		return c, invalidJSON()
	}
	if c.RoomID, ok = stringValue(v["room_id"]); !ok || !opaqueID(c.RoomID) {
		return c, invalidJSON()
	}
	if c.ExpectedRevision, ok = decimalValue(v["expected_revision"]); !ok {
		return c, invalidJSON()
	}
	if c.Context.GameID, ok = decimalValue(v["game_id"]); !ok {
		return c, invalidJSON()
	}
	if c.Context.RoundID, ok = decimalValue(v["round_id"]); !ok {
		return c, invalidJSON()
	}
	if c.Action, ok = stringValue(v["action"]); !ok {
		return c, invalidJSON()
	}
	switch c.Action {
	case "ready":
		payload, f := object(v["data"], "ready")
		if f != nil {
			return c, f
		}
		if string(payload["ready"]) == "null" || json.Unmarshal(payload["ready"], &c.Ready) != nil {
			return c, invalidJSON()
		}
	case "start":
		payload, f := object(v["data"], "seat_order", "shuffle_seats", "first_bidder")
		if f != nil {
			return c, f
		}
		if string(payload["shuffle_seats"]) == "null" || json.Unmarshal(payload["shuffle_seats"], &c.Start.ShuffleSeats) != nil {
			return c, invalidJSON()
		}
		if c.Start.ShuffleSeats {
			if string(payload["seat_order"]) != "null" {
				return c, fail(400, "INVALID_START", "Shuffled seating requires a null seat order.")
			}
		} else {
			var seats []json.RawMessage
			if json.Unmarshal(payload["seat_order"], &seats) != nil || len(seats) == 0 || len(seats) > game.DeckSize {
				return c, fail(400, "INVALID_START", "Specify the ready players' seat order.")
			}
			for _, raw := range seats {
				id, ok := stringValue(raw)
				if !ok || !opaqueID(id) {
					return c, invalidJSON()
				}
				c.Start.SeatOrder = append(c.Start.SeatOrder, game.PlayerID(id))
			}
		}
		if string(payload["first_bidder"]) != "null" {
			id, ok := stringValue(payload["first_bidder"])
			if !ok || !opaqueID(id) {
				return c, invalidJSON()
			}
			c.Start.FirstBidder = game.PlayerID(id)
		}
	case "bid":
		payload, f := object(v["data"], "bid")
		if f != nil {
			return c, f
		}
		if c.Bid, f = parseBid(payload["bid"]); f != nil {
			return c, f
		}
	case "challenge", "next_round":
		if _, f := object(v["data"]); f != nil {
			return c, f
		}
	default:
		return c, fail(400, "UNKNOWN_COMMAND", "The command action is unsupported.")
	}
	// Hash typed intent, not raw JSON: spacing/key order have no effect. The
	// decoder rejects unused fields, so no client-controlled content is lost.
	normalized, err := json.Marshal(c)
	if err != nil {
		return c, internal()
	}
	c.Fingerprint = sha256.Sum256(normalized)
	return c, nil
}

func parseBid(data []byte) (game.Bid, *failure) {
	var b game.Bid
	var values map[string]json.RawMessage
	if f := decodeJSON(data, &values); f != nil {
		return b, f
	}
	category, ok := stringValue(values["category"])
	categories := map[string]game.BidCategory{"high_card": game.HighCard, "pair": game.Pair, "two_pairs": game.TwoPairs, "low_straight": game.LowStraight, "high_straight": game.HighStraight, "three_of_a_kind": game.ThreeOfAKind, "full_house": game.FullHouse, "flush": game.Flush, "four_of_a_kind": game.FourOfAKind, "straight_flush": game.StraightFlush, "royal_straight_flush": game.RoyalStraightFlush}
	if !ok || categories[category] == 0 {
		return b, fail(400, "INVALID_BID", "Use a supported bid category.")
	}
	b.Category = categories[category]
	fields := []string{"category"}
	switch b.Category {
	case game.HighCard, game.Pair, game.ThreeOfAKind, game.FourOfAKind:
		fields = append(fields, "primary_rank")
	case game.TwoPairs, game.FullHouse:
		fields = append(fields, "primary_rank", "secondary_rank")
	case game.Flush, game.StraightFlush, game.RoyalStraightFlush:
		fields = append(fields, "suit")
	}
	values, f := object(data, fields...)
	if f != nil {
		return b, f
	}
	ranks := map[string]game.Rank{"9": game.Nine, "10": game.Ten, "J": game.Jack, "Q": game.Queen, "K": game.King, "A": game.Ace}
	suits := map[string]game.Suit{"clubs": game.Clubs, "diamonds": game.Diamonds, "hearts": game.Hearts, "spades": game.Spades}
	if raw, exists := values["primary_rank"]; exists {
		s, _ := stringValue(raw)
		b.PrimaryRank = ranks[s]
	}
	if raw, exists := values["secondary_rank"]; exists {
		s, _ := stringValue(raw)
		b.SecondaryRank = ranks[s]
	}
	if raw, exists := values["suit"]; exists {
		s, _ := stringValue(raw)
		b.Suit = suits[s]
	}
	if err := b.Validate(); err != nil {
		return b, fail(400, "INVALID_BID", "The bid parameters are invalid.")
	}
	return b, nil
}

func commandFailure(err error) *failure {
	// Stable public messages never include wrapped domain errors or private data.
	for _, item := range []struct {
		err           error
		status        int
		code, message string
	}{
		{room.ErrStaleGame, 409, "STALE_GAME", "The game has changed."},
		{room.ErrStaleRound, 409, "STALE_ROUND", "The round has changed."},
		{room.ErrStaleRevision, 409, "STALE_STATE", "The room has changed."},
		{room.ErrRequestIDReused, 409, "REQUEST_ID_REUSED", "The request ID was used for different content."},
		{room.ErrNoGame, 409, "NO_GAME", "No game has started."},
		{room.ErrNotAdmin, 403, "NOT_ADMIN", "Only the room admin can start a game."},
		{room.ErrGameInProgress, 409, "GAME_IN_PROGRESS", "A game is already in progress."},
		{room.ErrInvalidStart, 400, "INVALID_START", "The start choices must match the ready participants."},
		{game.ErrUnknownPlayer, 403, "NOT_PARTICIPANT", "This action requires participation."},
		{game.ErrInactivePlayer, 403, "INACTIVE_PLAYER", "This participant is inactive."},
		{game.ErrOutOfTurn, 409, "OUT_OF_TURN", "Wait for your turn."},
		{game.ErrWrongPhase, 409, "WRONG_PHASE", "This action is unavailable in this phase."},
		{game.ErrNoBid, 409, "NO_BID", "There is no preceding bid to challenge."},
		{game.ErrBidNotHigher, 409, "BID_NOT_HIGHER", "A new bid must strictly raise the preceding bid."},
		{game.ErrInvalidBid, 400, "INVALID_BID", "The bid parameters are invalid."},
	} {
		if errors.Is(err, item.err) {
			return fail(item.status, item.code, item.message)
		}
	}
	return roomFailure(err)
}
