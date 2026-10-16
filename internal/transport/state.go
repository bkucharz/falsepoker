package transport

import (
	"strconv"

	"github.com/bkucharz/falsepoker/internal/game"
	"github.com/bkucharz/falsepoker/internal/room"
)

// Wire DTOs are deliberately separate from domain state. In particular there
// is no undealt deck, numeric enum, credential, or random source to serialize.
type roomState struct {
	RoomID      string        `json:"room_id"`
	RoomCode    string        `json:"room_code"`
	Revision    string        `json:"revision"`
	GameID      string        `json:"game_id"`
	RecipientID string        `json:"recipient_id"`
	AdminID     string        `json:"admin_id"`
	Role        string        `json:"role"`
	Members     []memberState `json:"members"`
	Game        *gameState    `json:"game"`
}
type memberState struct {
	PlayerID    string `json:"player_id"`
	DisplayName string `json:"display_name"`
	Ready       bool   `json:"ready"`
}
type playerState struct {
	PlayerID         string      `json:"player_id"`
	DisplayName      string      `json:"display_name"`
	RequiredHandSize int         `json:"required_hand_size"`
	HandCount        int         `json:"hand_count"`
	Eliminated       bool        `json:"eliminated"`
	Withdrawn        bool        `json:"withdrawn"`
	Hand             []cardState `json:"hand"`
}
type gameState struct {
	Phase            string        `json:"phase"`
	RoundPhase       string        `json:"round_phase"`
	RoundID          string        `json:"round_id"`
	EliminationLimit int           `json:"elimination_limit"`
	StarterID        string        `json:"starter_id"`
	NextStarterID    *string       `json:"next_starter_id"`
	CurrentPlayerID  *string       `json:"current_player_id"`
	Players          []playerState `json:"players"`
	LastBid          *placedBid    `json:"last_bid"`
	LastResult       *roundResult  `json:"last_result"`
	WinnerID         *string       `json:"winner_id"`
}
type cardState struct {
	Rank string `json:"rank"`
	Suit string `json:"suit"`
}
type bidState struct {
	Category      string `json:"category"`
	PrimaryRank   string `json:"primary_rank,omitempty"`
	SecondaryRank string `json:"secondary_rank,omitempty"`
	Suit          string `json:"suit,omitempty"`
}
type placedBid struct {
	PlayerID string   `json:"player_id"`
	Bid      bidState `json:"bid"`
}
type revealedHand struct {
	PlayerID string      `json:"player_id"`
	Cards    []cardState `json:"cards"`
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

func toRoomState(view room.RoomView) roomState {
	state := roomState{
		RoomID: view.RoomID, RoomCode: view.Code, Revision: counter(view.Revision), GameID: counter(view.GameID),
		RecipientID: string(view.Recipient), AdminID: string(view.Admin), Role: "spectator",
		Members: make([]memberState, len(view.Members)),
	}
	if view.Role == room.Participant {
		state.Role = "participant"
	}
	for i, member := range view.Members {
		state.Members[i] = memberState{string(member.ID), member.DisplayName, member.Ready}
	}
	if view.Game == nil {
		return state
	}
	g := view.Game
	wire := &gameState{
		Phase:      map[game.GamePhase]string{game.GamePlaying: "playing", game.GameFinished: "finished"}[g.Phase],
		RoundPhase: map[game.RoundPhase]string{game.RoundBidding: "bidding", game.RoundResolution: "resolution"}[g.RoundPhase],
		RoundID:    counter(g.RoundID), EliminationLimit: g.EliminationLimit, StarterID: string(g.Starter),
		NextStarterID: optionalID(g.NextStarter), CurrentPlayerID: optionalID(g.CurrentPlayer), WinnerID: optionalID(g.Winner),
		Players: make([]playerState, len(g.Players)),
	}
	for i, player := range g.Players {
		p := playerState{PlayerID: string(player.ID), DisplayName: player.DisplayName, RequiredHandSize: player.RequiredHandSize, HandCount: player.HandCount, Eliminated: player.Eliminated, Withdrawn: player.Withdrawn}
		if player.HandCount == 0 || player.Hand != nil {
			p.Hand = cards(player.Hand)
		}
		wire.Players[i] = p
	}
	if g.LastBid != nil {
		b := convertPlacedBid(*g.LastBid)
		wire.LastBid = &b
	}
	if g.LastResult != nil {
		r := g.LastResult
		result := &roundResult{
			RoundID: counter(r.RoundID), EliminationLimit: r.EliminationLimit, ChallengedBid: convertPlacedBid(r.ChallengedBid),
			ChallengerID: string(r.Challenger), ClaimSatisfied: r.ClaimSatisfied, LoserID: string(r.Loser), RevealedHands: make([]revealedHand, len(r.RevealedHands)),
		}
		for i, hand := range r.RevealedHands {
			result.RevealedHands[i] = revealedHand{string(hand.PlayerID), cards(hand.Cards)}
		}
		wire.LastResult = result
	}
	state.Game = wire
	return state
}

func counter(value uint64) string { return strconv.FormatUint(value, 10) }
func optionalID(id game.PlayerID) *string {
	if id == "" {
		return nil
	}
	value := string(id)
	return &value
}
func cards(source []game.Card) []cardState {
	result := make([]cardState, len(source))
	for i, card := range source {
		result[i] = cardState{rankName(card.Rank), suitName(card.Suit)}
	}
	return result
}
func rankName(rank game.Rank) string {
	return map[game.Rank]string{game.Nine: "9", game.Ten: "10", game.Jack: "J", game.Queen: "Q", game.King: "K", game.Ace: "A"}[rank]
}
func suitName(suit game.Suit) string {
	return map[game.Suit]string{game.Clubs: "clubs", game.Diamonds: "diamonds", game.Hearts: "hearts", game.Spades: "spades"}[suit]
}
func convertPlacedBid(b game.PlacedBid) placedBid {
	category := map[game.BidCategory]string{
		game.HighCard: "high_card", game.Pair: "pair", game.TwoPairs: "two_pairs", game.LowStraight: "low_straight", game.HighStraight: "high_straight",
		game.ThreeOfAKind: "three_of_a_kind", game.FullHouse: "full_house", game.Flush: "flush", game.FourOfAKind: "four_of_a_kind",
		game.StraightFlush: "straight_flush", game.RoyalStraightFlush: "royal_straight_flush",
	}[b.Bid.Category]
	return placedBid{string(b.PlayerID), bidState{category, rankName(b.Bid.PrimaryRank), rankName(b.Bid.SecondaryRank), suitName(b.Bid.Suit)}}
}
