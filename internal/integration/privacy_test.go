package integration_test

import (
	"encoding/json"
	"testing"
)

// Adversarial fixtures prove the client assertions detect leaks even when cards
// are valid and public state otherwise looks plausible. This guards the oracle,
// not just the current server implementation.
func TestPrivacyChecksDetectLeaks(t *testing.T) {
	h := newHarness(t)
	g := h.group("Alice", "Bob")
	a, b := g.connections[1], g.connections[2]
	g.start([]*connection{a, b}, a)
	clone := func(s snapshot) snapshot {
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		var copied snapshot
		if err := decode(data, &copied); err != nil {
			t.Fatal(err)
		}
		return copied
	}
	tests := []struct {
		name   string
		mutate func(*snapshot)
	}{
		{"opponent hand", func(s *snapshot) { s.Game.Players[1].Hand = append([]card(nil), g.peek().Game.Players[1].Hand...) }},
		{"spectator privilege escalation", func(s *snapshot) { s.Role = "spectator" }},
		{"wrong recipient", func(s *snapshot) { s.RecipientID = b.actor.id }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bad := clone(a.state)
			tt.mutate(&bad)
			if err := validateSnapshot(bad, a.actor.id, g.m); err == nil {
				t.Fatal("privacy validator accepted injected leak")
			}
		})
	}
	t.Run("valid but wrong own card", func(t *testing.T) {
		bad := clone(a.state)
		original := bad.Game.Players[0].Hand[0]
		replacement := card{Rank: "A", Suit: "spades"}
		if replacement == original {
			replacement.Rank = "9"
		}
		bad.Game.Players[0].Hand[0] = replacement
		if err := validateSnapshot(bad, a.actor.id, g.m); err != nil {
			t.Fatalf("fixture should pass shape/visibility checks: %v", err)
		}
		if err := compareAudience(bad, g.peek()); err == nil {
			t.Fatal("observer comparison accepted wrong private cards")
		}
	})
	t.Run("undealt field", func(t *testing.T) {
		encoded, err := json.Marshal(a.state)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		fields["game"].(map[string]any)["undealt"] = []card{{Rank: "A", Suit: "spades"}}
		encoded, err = json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		var state snapshot
		if err := decode(encoded, &state); err == nil {
			t.Fatal("wire allowlist accepted an undealt deck")
		}
	})
	t.Run("session credential", func(t *testing.T) {
		h.mu.Lock()
		secret := ""
		for value := range h.secrets {
			secret = value
			break
		}
		h.mu.Unlock()
		if secret == "" || h.inspect([]byte(secret)) == nil {
			t.Fatal("credential leak detector failed")
		}
	})
}
