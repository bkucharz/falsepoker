package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
)

// These are client-owned protocol types, deliberately independent of transport
// DTOs and engine state. Strict decoding makes unexpected private fields fail.
type membership struct {
	RoomCode string `json:"room_code"`
	RoomID   string `json:"room_id"`
	PlayerID string `json:"player_id"`
	Revision string `json:"revision"`
}
type snapshot struct {
	RoomID      string     `json:"room_id"`
	RoomCode    string     `json:"room_code"`
	Revision    string     `json:"revision"`
	GameID      string     `json:"game_id"`
	RecipientID string     `json:"recipient_id"`
	AdminID     string     `json:"admin_id"`
	Role        string     `json:"role"`
	Members     []member   `json:"members"`
	Game        *gameState `json:"game"`
}
type member struct {
	PlayerID    string `json:"player_id"`
	DisplayName string `json:"display_name"`
	Ready       bool   `json:"ready"`
}
type card struct {
	Rank string `json:"rank"`
	Suit string `json:"suit"`
}
type bid struct {
	Category      string `json:"category"`
	PrimaryRank   string `json:"primary_rank,omitempty"`
	SecondaryRank string `json:"secondary_rank,omitempty"`
	Suit          string `json:"suit,omitempty"`
}
type placedBid struct {
	PlayerID string `json:"player_id"`
	Bid      bid    `json:"bid"`
}
type player struct {
	PlayerID         string `json:"player_id"`
	DisplayName      string `json:"display_name"`
	RequiredHandSize int    `json:"required_hand_size"`
	HandCount        int    `json:"hand_count"`
	Eliminated       bool   `json:"eliminated"`
	Withdrawn        bool   `json:"withdrawn"`
	Hand             []card `json:"hand"`
}
type revealedHand struct {
	PlayerID string `json:"player_id"`
	Cards    []card `json:"cards"`
}
type roundResult struct {
	RoundID          string         `json:"round_id"`
	EliminationLimit int            `json:"elimination_limit"`
	ChallengedBid    placedBid      `json:"challenged_bid"`
	ChallengerID     string         `json:"challenger_id"`
	ClaimSatisfied   bool           `json:"claim_satisfied"`
	LoserID          string         `json:"loser_id"`
	RevealedHands    []revealedHand `json:"revealed_hands"`
}
type gameState struct {
	Phase            string       `json:"phase"`
	RoundPhase       string       `json:"round_phase"`
	RoundID          string       `json:"round_id"`
	EliminationLimit int          `json:"elimination_limit"`
	StarterID        string       `json:"starter_id"`
	NextStarterID    *string      `json:"next_starter_id"`
	CurrentPlayerID  *string      `json:"current_player_id"`
	Players          []player     `json:"players"`
	LastBid          *placedBid   `json:"last_bid"`
	LastResult       *roundResult `json:"last_result"`
	WinnerID         *string      `json:"winner_id"`
}
type problem struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}
type message struct {
	Type         string    `json:"type"`
	State        *snapshot `json:"state"`
	RequestID    *string   `json:"request_id"`
	RoomID       string    `json:"room_id"`
	Revision     string    `json:"revision"`
	Changed      *bool     `json:"changed"`
	Replayed     bool      `json:"replayed"`
	Error        *problem  `json:"error"`
	RetryAfterMS int64     `json:"retry_after_ms"`
}
type command struct {
	Type             string `json:"type"`
	RequestID        string `json:"request_id"`
	RoomID           string `json:"room_id"`
	ExpectedRevision string `json:"expected_revision"`
	GameID           string `json:"game_id"`
	RoundID          string `json:"round_id"`
	Action           string `json:"action"`
	Data             any    `json:"data"`
}

func decode(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
func number(s string) (uint64, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != s {
		return 0, fmt.Errorf("noncanonical counter %q", s)
	}
	return n, nil
}
func increment(s string) string {
	n, err := number(s)
	if err != nil {
		panic(err)
	}
	return strconv.FormatUint(n+1, 10)
}
func active(p player) bool { return !p.Eliminated && !p.Withdrawn }
func findPlayer(g *gameState, id string) player {
	for _, p := range g.Players {
		if p.PlayerID == id {
			return p
		}
	}
	panic("player absent from protocol roster")
}

func validateCards(cards []card, seen map[card]bool) error {
	for _, c := range cards {
		rankOK := c.Rank == "9" || c.Rank == "10" || c.Rank == "J" || c.Rank == "Q" || c.Rank == "K" || c.Rank == "A"
		suitOK := c.Suit == "clubs" || c.Suit == "diamonds" || c.Suit == "hearts" || c.Suit == "spades"
		if !rankOK || !suitOK || seen[c] {
			return fmt.Errorf("invalid or duplicate visible card")
		}
		seen[c] = true
	}
	return nil
}

func validateSnapshot(s snapshot, recipient string, m membership) error {
	if s.RoomID != m.RoomID || s.RoomCode != m.RoomCode || s.RecipientID != recipient {
		return fmt.Errorf("wrong room or recipient")
	}
	if n, err := number(s.Revision); err != nil || n == 0 {
		return fmt.Errorf("invalid room revision")
	}
	if _, err := number(s.GameID); err != nil {
		return err
	}
	members := make(map[string]bool)
	for _, member := range s.Members {
		if member.PlayerID == "" || member.DisplayName == "" || members[member.PlayerID] {
			return fmt.Errorf("invalid member roster")
		}
		members[member.PlayerID] = true
	}
	if !members[recipient] || !members[s.AdminID] {
		return fmt.Errorf("recipient/admin missing from membership")
	}
	if s.Game == nil {
		if s.GameID != "0" || s.Role != "spectator" {
			return fmt.Errorf("invalid lobby view")
		}
		return nil
	}
	g := s.Game
	if s.GameID == "0" || g.EliminationLimit < 2 || g.EliminationLimit > 6 || len(g.Players) < 2 || len(g.Players) > 24 {
		return fmt.Errorf("invalid game metadata")
	}
	currentRound, err := number(g.RoundID)
	if err != nil || currentRound == 0 {
		return fmt.Errorf("invalid round ID")
	}
	participant := false
	players := make(map[string]bool)
	survivors := make(map[string]bool)
	total := 0
	for _, p := range g.Players {
		if p.PlayerID == "" || players[p.PlayerID] || p.RequiredHandSize < 1 || p.HandCount < 0 || p.HandCount > 5 {
			return fmt.Errorf("invalid game roster/count")
		}
		players[p.PlayerID] = true
		survivors[p.PlayerID] = active(p)
		total += p.HandCount
		if p.PlayerID == recipient && active(p) {
			participant = true
		}
	}
	expectedRole := "spectator"
	if participant {
		expectedRole = "participant"
	}
	if s.Role != expectedRole || total > 24 {
		return fmt.Errorf("wrong recipient role or dealt count")
	}
	reveal := g.Phase == "finished" || g.RoundPhase == "resolution"
	seen := make(map[card]bool)
	for _, p := range g.Players {
		visible := reveal || (participant && p.PlayerID == recipient) || (!participant && active(p))
		switch {
		case p.HandCount == 0:
			if p.Hand == nil || len(p.Hand) != 0 {
				return fmt.Errorf("zero-card hand must be []")
			}
		case visible:
			if len(p.Hand) != p.HandCount {
				return fmt.Errorf("authorized hand missing or wrong size")
			}
		default:
			if p.Hand != nil {
				return fmt.Errorf("private opponent/withdrawn hand exposed")
			}
		}
		if err := validateCards(p.Hand, seen); err != nil {
			return err
		}
	}
	if g.Phase == "playing" {
		if g.WinnerID != nil {
			return fmt.Errorf("winner before finish")
		}
		switch g.RoundPhase {
		case "bidding":
			if g.CurrentPlayerID == nil || !survivors[*g.CurrentPlayerID] || g.NextStarterID != nil {
				return fmt.Errorf("invalid bidding turn")
			}
		case "resolution":
			if g.CurrentPlayerID != nil || g.NextStarterID == nil || !survivors[*g.NextStarterID] || g.LastResult == nil {
				return fmt.Errorf("invalid resolution starter/result")
			}
		default:
			return fmt.Errorf("unknown round phase")
		}
	} else if g.Phase == "finished" {
		count := 0
		for _, survivor := range survivors {
			if survivor {
				count++
			}
		}
		if count != 1 || g.WinnerID == nil || !survivors[*g.WinnerID] || g.RoundPhase != "resolution" || g.CurrentPlayerID != nil || g.NextStarterID != nil {
			return fmt.Errorf("invalid terminal state")
		}
	} else {
		return fmt.Errorf("unknown game phase")
	}
	if g.LastBid != nil && !players[g.LastBid.PlayerID] {
		return fmt.Errorf("bidder absent from game roster")
	}
	if result := g.LastResult; result != nil {
		round, err := number(result.RoundID)
		if err != nil || round == 0 || round > currentRound || !players[result.LoserID] || !players[result.ChallengerID] {
			return fmt.Errorf("invalid completed result")
		}
		seen := make(map[card]bool)
		owners := make(map[string]bool)
		for _, hand := range result.RevealedHands {
			if !players[hand.PlayerID] || owners[hand.PlayerID] || len(hand.Cards) == 0 {
				return fmt.Errorf("invalid reveal ownership")
			}
			owners[hand.PlayerID] = true
			if err := validateCards(hand.Cards, seen); err != nil {
				return err
			}
		}
		if len(seen) > 24 {
			return fmt.Errorf("reveal exceeds deck")
		}
	}
	return nil
}

// Compare every recipient at a committed revision with an authorized observer.
// Public state must agree, visible cards must match, hidden hands stay null.
func compareAudience(got, oracle snapshot) error {
	if (got.Game == nil) != (oracle.Game == nil) || (got.Game != nil && len(got.Game.Players) != len(oracle.Game.Players)) {
		return fmt.Errorf("recipient game roster differs")
	}
	if got.Game != nil {
		for i, p := range got.Game.Players {
			if p.Hand != nil && !reflect.DeepEqual(p.Hand, oracle.Game.Players[i].Hand) {
				return fmt.Errorf("recipient cards differ from observer at revision %s", got.Revision)
			}
		}
	}
	public := func(s snapshot) snapshot {
		s.RecipientID = ""
		s.Role = ""
		if s.Game != nil {
			g := *s.Game
			g.Players = append([]player(nil), g.Players...)
			for i := range g.Players {
				g.Players[i].Hand = nil
			}
			s.Game = &g
		}
		return s
	}
	if !reflect.DeepEqual(public(got), public(oracle)) {
		return fmt.Errorf("recipient public state differs at revision %s", got.Revision)
	}
	return nil
}
