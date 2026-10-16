package integration_test

import (
	"reflect"
	"testing"
)

// The observer is authorized to see survivors' dealt cards. Claim selection
// intentionally does not call the engine's evaluator: one actually visible rank
// proves high-card truth; fewer than five cards of a suit disproves its royal.
func (g *group) trueBid() bid {
	g.h.t.Helper()
	s := g.peek()
	id := *s.Game.CurrentPlayerID
	p := findPlayer(s.Game, id)
	if len(p.Hand) == 0 {
		g.h.t.Fatal("current bidder has no visible card")
	}
	return bid{Category: "high_card", PrimaryRank: p.Hand[0].Rank}
}
func (g *group) falseBid() bid {
	g.h.t.Helper()
	counts := make(map[string]int)
	for _, p := range g.peek().Game.Players {
		for _, c := range p.Hand {
			counts[c.Suit]++
		}
	}
	for _, suit := range []string{"clubs", "diamonds", "hearts", "spades"} {
		if counts[suit] < 5 {
			return bid{Category: "royal_straight_flush", Suit: suit}
		}
	}
	g.h.t.Fatal("fixture cannot prove a false royal claim")
	return bid{}
}
func (g *group) place(b bid) (*connection, command, message) {
	g.h.t.Helper()
	c := g.byID(*g.observer.state.Game.CurrentPlayerID)
	before := g.observer.state.Game
	next := ""
	for i, p := range before.Players {
		if p.PlayerID == c.actor.id {
			for offset := 1; offset < len(before.Players); offset++ {
				candidate := before.Players[(i+offset)%len(before.Players)]
				if active(candidate) {
					next = candidate.PlayerID
					break
				}
			}
		}
	}
	cmd, ack := g.change(c, "bid", map[string]any{"bid": b})
	if got := g.observer.state.Game.CurrentPlayerID; got == nil || *got != next {
		g.h.t.Fatal("bid did not advance in seat order, skipping inactive players")
	}
	if got := g.observer.state.Game.LastBid; got == nil || got.PlayerID != c.actor.id || got.Bid != b {
		g.h.t.Fatal("bid was not recorded")
	}
	return c, cmd, ack
}
func (g *group) challenge(truth bool) (command, message) {
	g.h.t.Helper()
	before := g.peek()
	challenger := g.byID(*before.Game.CurrentPlayerID)
	cmd, ack := g.change(challenger, "challenge", struct{}{})
	g.checkResolution(before, challenger.actor.id, truth)
	return cmd, ack
}
func (g *group) checkResolution(before snapshot, challenger string, truth bool) {
	g.h.t.Helper()
	after := g.peek()
	old, new := before.Game, after.Game
	loser := old.LastBid.PlayerID
	if truth {
		loser = challenger
	}
	result := new.LastResult
	if new.RoundPhase != "resolution" || new.RoundID != old.RoundID || new.CurrentPlayerID != nil || result == nil || result.RoundID != old.RoundID || result.ClaimSatisfied != truth || result.LoserID != loser || result.ChallengerID != challenger || result.ChallengedBid != *old.LastBid || result.EliminationLimit != old.EliminationLimit {
		g.h.t.Fatal("challenge outcome differs from independently selected claim")
	}
	wantReveal := make([]revealedHand, 0, len(old.Players))
	for i, p := range old.Players {
		if p.HandCount > 0 {
			wantReveal = append(wantReveal, revealedHand{p.PlayerID, p.Hand})
		}
		got := new.Players[i]
		wantSize := p.RequiredHandSize
		wantEliminated := p.Eliminated
		if p.PlayerID == loser && active(p) {
			if wantSize+1 >= old.EliminationLimit {
				wantEliminated = true
			} else {
				wantSize++
			}
		}
		if got.RequiredHandSize != wantSize || got.Eliminated != wantEliminated || got.Withdrawn != p.Withdrawn || got.HandCount != p.HandCount || !reflect.DeepEqual(got.Hand, p.Hand) {
			g.h.t.Fatal("penalty/elimination changed the wrong player or the revealed cards")
		}
	}
	if !reflect.DeepEqual(result.RevealedHands, wantReveal) {
		g.h.t.Fatal("public reveal differs from the dealt cards")
	}
	survivors := 0
	winner := ""
	for _, p := range new.Players {
		if active(p) {
			survivors++
			winner = p.PlayerID
		}
	}
	if survivors == 1 {
		if new.Phase != "finished" || new.WinnerID == nil || *new.WinnerID != winner || new.NextStarterID != nil {
			g.h.t.Fatal("last survivor did not win immediately")
		}
		for _, member := range after.Members {
			if member.Ready {
				g.h.t.Fatal("finishing transition retained readiness")
			}
		}
	} else {
		next := loser
		for i, p := range new.Players {
			if p.PlayerID == loser && !active(p) {
				for offset := 1; offset < len(new.Players); offset++ {
					candidate := new.Players[(i+offset)%len(new.Players)]
					if active(candidate) {
						next = candidate.PlayerID
						break
					}
				}
			}
		}
		if new.Phase != "playing" || new.NextStarterID == nil || *new.NextStarterID != next {
			g.h.t.Fatal("wrong next starter after loss/elimination")
		}
	}
	lost := findPlayer(new, loser)
	g.h.t.Logf("game %s round %s: %s loses; claim satisfied=%t, required hand=%d, eliminated=%t", after.GameID, new.RoundID, lost.DisplayName, truth, lost.RequiredHandSize, lost.Eliminated)
}
func (g *group) advance() {
	g.h.t.Helper()
	before := g.peek()
	starter := *before.Game.NextStarterID
	g.change(g.byID(starter), "next_round", struct{}{})
	after := g.peek()
	if after.Game.RoundID != increment(before.Game.RoundID) || after.Game.StarterID != starter || after.Game.CurrentPlayerID == nil || *after.Game.CurrentPlayerID != starter || after.Game.LastBid != nil || !reflect.DeepEqual(after.Game.LastResult, before.Game.LastResult) {
		g.h.t.Fatal("new round lost the previous reveal or starter/context")
	}
	count := 0
	for _, p := range after.Game.Players {
		if active(p) {
			count++
			if p.HandCount != p.RequiredHandSize {
				g.h.t.Fatal("survivor received wrong hand size")
			}
		} else if p.HandCount != 0 {
			g.h.t.Fatal("inactive player received cards")
		}
	}
	if after.Game.EliminationLimit != min(6, 24/count+1) {
		g.h.t.Fatal("round elimination limit was not recalculated")
	}
}
func (g *group) loseUntilEliminated(id string) {
	g.h.t.Helper()
	for losses := 0; losses < 5; losses++ {
		if *g.observer.state.Game.CurrentPlayerID != id {
			g.h.t.Fatal("wrong bidder while targeting repeated losses")
		}
		g.place(g.falseBid())
		g.challenge(false)
		if findPlayer(g.observer.state.Game, id).Eliminated {
			return
		}
		g.advance()
	}
	g.h.t.Fatal("five losses did not eliminate a one-card participant")
}

func TestCompleteGameAndReplay(t *testing.T) {
	h := newHarness(t)
	g := h.group("Alice", "Bob", "Cara")
	a, b, c := g.connections[1], g.connections[2], g.connections[3]
	start, started := g.start([]*connection{a, b, c}, a)
	// A true challenge penalizes Bob, keeping the original one-card reveal.
	g.place(g.trueBid())
	g.challenge(true)
	if findPlayer(g.observer.state.Game, b.actor.id).RequiredHandSize != 2 {
		t.Fatal("true challenge did not penalize challenger")
	}
	g.advance()
	// Bob -> Cara -> Alice: opening and strict raises reach a known false claim.
	g.place(bid{Category: "high_card", PrimaryRank: "9"})
	g.place(bid{Category: "pair", PrimaryRank: "9"})
	g.place(g.falseBid())
	g.challenge(false)
	g.advance()
	g.loseUntilEliminated(a.actor.id)
	if a.state.Role != "spectator" || g.observer.state.Game.Phase != "playing" {
		t.Fatal("eliminated Alice did not remain a viewer")
	}
	g.advance()
	g.loseUntilEliminated(b.actor.id)
	final := g.observer.state
	if final.Game.Phase != "finished" || *final.Game.WinnerID != c.actor.id || b.state.Role != "spectator" || c.state.Role != "participant" {
		t.Fatal("wrong winner or final viewer roles")
	}
	// A replay uses the same room/connections, fresh identity/history/hands, and
	// makes the formerly spectating admin an ordinary private-hand participant.
	g.start([]*connection{g.observer, a, c}, g.observer)
	if g.observer.state.GameID != increment(final.GameID) || g.observer.state.Game.LastResult != nil || g.observer.state.Game.WinnerID != nil || g.observer.state.Role != "participant" {
		t.Fatal("replay retained previous game state or spectator permissions")
	}
	if a.state.Role != "participant" || findPlayer(a.state.Game, a.actor.id).Eliminated {
		t.Fatal("replay retained Alice's elimination/spectator access")
	}
	before := g.observer.actor.view(g.m)
	replayed := g.observer.apply(start)
	requireAck(t, replayed)
	if !replayed.Replayed || replayed.Revision != started.Revision || !reflect.DeepEqual(g.observer.actor.view(g.m), before) {
		t.Fatal("old start retry acted on the replayed game")
	}
	g.loseUntilEliminated(g.observer.actor.id)
	g.advance()
	g.loseUntilEliminated(a.actor.id)
	if got := g.observer.state; got.Game.Phase != "finished" || *got.Game.WinnerID != c.actor.id {
		t.Fatal("second full game failed")
	}
	t.Log("completed two games with true/false challenges, raises, penalties, elimination, public reveals, and replay privacy")
}

func TestDeterministicDealsAndIndependentRooms(t *testing.T) {
	h := newHarness(t)
	one := h.group("Alice", "Bob")
	a, b := one.connections[1], one.connections[2]
	one.startRandom([]*connection{a, b})
	// Use the same identities and random choices in another room. Both rooms use
	// seed 42, so equal initial deals prove deterministic setup and separate RNGs.
	m := one.observer.actor.create()
	two := &group{h: h, m: m}
	two.observer = one.observer.actor.connect(m)
	two.connections = append(two.connections, two.observer)
	a2 := two.add(a.actor)
	b2 := two.add(b.actor)
	two.startRandom([]*connection{a2, b2})
	if !reflect.DeepEqual(one.peek().Game.Players, two.peek().Game.Players) {
		t.Fatal("seeded initial deals differ")
	}
	untouched := two.observer.actor.view(two.m)
	target := *one.observer.state.Game.CurrentPlayerID
	if *two.observer.state.Game.CurrentPlayerID != target {
		t.Fatal("seeded random starters differ")
	}
	one.loseUntilEliminated(target)
	if !reflect.DeepEqual(two.observer.actor.view(two.m), untouched) {
		t.Fatal("playing another room changed this room's state")
	}
	two.loseUntilEliminated(target)
	if !reflect.DeepEqual(one.peek().Game.Players, two.peek().Game.Players) {
		t.Fatal("another room consumed this room's shuffle stream")
	}
	// Identical scoped request IDs in separate rooms must not share outcomes.
	cmd := one.observer.command("ready", map[string]bool{"ready": true})
	ack := one.observer.apply(cmd)
	requireAck(t, ack)
	one.sync(ack.Revision)
	other := two.observer.command("ready", map[string]bool{"ready": true})
	other.RequestID = cmd.RequestID
	ack2 := two.observer.apply(other)
	requireAck(t, ack2)
	two.sync(ack2.Revision)
	if ack2.Replayed {
		t.Fatal("request cache leaked across room lifetimes")
	}
	// The same ID is also independent for different players in the same room.
	perPlayer := a.command("ready", map[string]bool{"ready": true})
	perPlayer.RequestID = cmd.RequestID
	playerAck := a.apply(perPlayer)
	requireAck(t, playerAck)
	one.sync(playerAck.Revision)
	if playerAck.Replayed {
		t.Fatal("request cache leaked across player identities")
	}
}
