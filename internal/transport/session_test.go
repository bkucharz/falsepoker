package transport

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/bkucharz/falsepoker/internal/room"
	"github.com/bkucharz/falsepoker/internal/testclock"
)

type controlledClock struct{ *testclock.Clock }

func newClock() *controlledClock {
	return &controlledClock{testclock.New()}
}
func (c *controlledClock) AfterFunc(d time.Duration, callback func()) room.Timer {
	return c.Clock.AfterFunc(d, callback)
}
func (c *controlledClock) advance(d time.Duration) { c.Advance(d) }

func TestHTTPRetryConflictAndRejectedOutcome(t *testing.T) {
	f := newFixture(t, Config{Limits: Limits{MaxMemberships: 1}}, false)
	client, _ := f.actor(t)
	key := f.key()
	original := f.request(t, client, "POST", "/api/v1/rooms", `{"display_name":" Alice "}`, key, nil)
	if original.status != 201 {
		t.Fatal(original.body)
	}
	retry := f.request(t, client, "POST", "/api/v1/rooms", "{ \n \"display_name\": \"Alice\" }", key, nil)
	if retry.body != original.body || retry.status != 201 || retry.header.Get("Idempotency-Replayed") != "true" || retry.header.Get("Location") != original.header.Get("Location") {
		t.Fatal("normalized retry did not preserve the original response")
	}
	errorCode(t, f.request(t, client, "POST", "/api/v1/rooms", `{"display_name":"Bob"}`, key, nil), 409, "REQUEST_ID_REUSED")
	var membership membershipResponse
	if err := json.Unmarshal([]byte(original.body), &membership); err != nil {
		t.Fatal(err)
	}
	otherKey := f.key()
	denied := f.request(t, client, "POST", "/api/v1/rooms", `{"display_name":"Second"}`, otherKey, nil)
	errorCode(t, denied, 409, "MEMBERSHIP_LIMIT")
	left := f.request(t, client, "POST", "/api/v1/rooms/"+membership.RoomCode+"/leave", `{"room_id":"`+membership.RoomID+`"}`, f.key(), nil)
	if left.status != 200 {
		t.Fatal(left.body)
	}
	stillDenied := f.request(t, client, "POST", "/api/v1/rooms", `{"display_name":"Second"}`, otherKey, nil)
	if stillDenied.body != denied.body || stillDenied.header.Get("Idempotency-Replayed") != "true" {
		t.Fatal("cached rejection changed into an accepted action")
	}
	if fresh := f.request(t, client, "POST", "/api/v1/rooms", `{"display_name":"Second"}`, f.key(), nil); fresh.status != 201 {
		t.Fatal("a newly reviewed action could not proceed after leaving")
	}
}

func TestHTTPRetryStorageIsBoundedWithoutEarlyEviction(t *testing.T) {
	clock := newClock()
	f := newFixture(t, Config{Clock: clock, Limits: Limits{MaxRetryRecords: 1}}, false)
	client, _ := f.actor(t)
	key := f.key()
	first := f.request(t, client, "POST", "/api/v1/rooms", `{"display_name":"First"}`, key, nil)
	if first.status != 201 {
		t.Fatal(first.body)
	}
	secondKey := f.key()
	full := f.request(t, client, "POST", "/api/v1/rooms", `{"display_name":"Second"}`, secondKey, nil)
	errorCode(t, full, 429, "RETRY_WINDOW_FULL")
	if full.header.Get("Retry-After") == "" {
		t.Fatal("cache admission failure omitted Retry-After")
	}
	clock.advance(4 * time.Minute)
	replayed := f.request(t, client, "POST", "/api/v1/rooms", `{"display_name":"First"}`, key, nil)
	if replayed.body != first.body || replayed.header.Get("Idempotency-Replayed") != "true" {
		t.Fatal("unexpired request was evicted or changed")
	}
	clock.advance(time.Minute)
	after := f.request(t, client, "POST", "/api/v1/rooms", `{"display_name":"Second"}`, secondKey, nil)
	if after.status != 201 {
		t.Fatalf("expired records were not released at the original deadline: %s", after.body)
	}
}

func TestHTTPRoomCapacityRejectsWithoutCachingTransientFailure(t *testing.T) {
	registry, err := room.NewRegistryWithLimits(1, 64)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, Config{Registry: registry}, false)
	alice, _ := f.actor(t)
	bob, _ := f.actor(t)
	first := f.create(t, alice, "Alice")
	key := f.key()
	busy := f.request(t, bob, "POST", "/api/v1/rooms", `{"display_name":"Bob"}`, key, nil)
	errorCode(t, busy, 503, "SERVER_BUSY")
	if busy.header.Get("Retry-After") == "" || !strings.Contains(busy.body, `"retryable":true`) {
		t.Fatal("server capacity failure was not retryable")
	}
	if left := f.request(t, alice, "POST", "/api/v1/rooms/"+first.RoomCode+"/leave", `{"room_id":"`+first.RoomID+`"}`, f.key(), nil); left.status != 200 {
		t.Fatal(left.body)
	}
	after := f.request(t, bob, "POST", "/api/v1/rooms", `{"display_name":"Bob"}`, key, nil)
	if after.status != 201 || after.header.Get("Idempotency-Replayed") != "" {
		t.Fatal("transient capacity rejection was incorrectly cached")
	}
}

func TestHTTPIdleSessionsExpireButMembersRemain(t *testing.T) {
	clock := newClock()
	f := newFixture(t, Config{Clock: clock}, false)
	idle, _ := f.actor(t)
	member, memberID := f.actor(t)
	created := f.create(t, member, "Member")
	// A live connection retains membership; an abandoned HTTP join now expires
	// after its initial attachment grace instead of retaining a session forever.
	f.connect(t, member, created)
	clock.advance(24 * time.Hour)
	expired := f.request(t, idle, "POST", "/api/v1/session", "{}", "", nil)
	errorCode(t, expired, 401, "SESSION_EXPIRED")
	if len(expired.cookies) != 1 || expired.cookies[0].MaxAge != -1 {
		t.Fatal("expired cookie was not cleared")
	}
	active := f.request(t, member, "POST", "/api/v1/session", "{}", "", nil)
	if active.status != 200 || !strings.Contains(active.body, memberID) {
		t.Fatal("membership did not retain the session identity")
	}
	path := "/api/v1/rooms/" + created.RoomCode
	if result := f.request(t, member, "POST", path+"/leave", `{"room_id":"`+created.RoomID+`"}`, f.key(), nil); result.status != 200 {
		t.Fatal(result.body)
	}
	clock.advance(24 * time.Hour)
	errorCode(t, f.request(t, member, "GET", path, "", "", nil), 401, "SESSION_EXPIRED")
	newSession := f.request(t, member, "POST", "/api/v1/session", "{}", "", nil)
	if newSession.status != 201 || strings.Contains(newSession.body, memberID) {
		t.Fatal("explicit reinitialization restored an expired identity")
	}
}

func TestHTTPSessionCapacityAndCleanup(t *testing.T) {
	clock := newClock()
	f := newFixture(t, Config{Clock: clock, Limits: Limits{MaxSessions: 1}}, false)
	first, _ := f.actor(t)
	other := f.client(t)
	errorCode(t, f.request(t, other, "POST", "/api/v1/session", "{}", "", nil), 503, "SERVER_BUSY")
	clock.advance(24 * time.Hour)
	if result := f.request(t, other, "POST", "/api/v1/session", "{}", "", nil); result.status != 201 {
		t.Fatal("session capacity did not recover after expiry")
	}
	errorCode(t, f.request(t, first, "POST", "/api/v1/session", "{}", "", nil), 401, "SESSION_EXPIRED")
}

func TestHTTPIngressLimitsIncludeCachedRetries(t *testing.T) {
	clock := newClock()
	f := newFixture(t, Config{Clock: clock, Limits: Limits{MutationRate: 1, MutationBurst: 1, BootstrapRate: 1, BootstrapBurst: 1}}, false)
	client, _ := f.actor(t)
	other := f.client(t)
	errorCode(t, f.request(t, other, "POST", "/api/v1/session", "{}", "", nil), 429, "RATE_LIMITED")
	clock.advance(time.Second)
	if result := f.request(t, other, "POST", "/api/v1/session", "{}", "", nil); result.status != 201 {
		t.Fatal("bootstrap token budget did not replenish")
	}
	key := f.key()
	first := f.request(t, client, "POST", "/api/v1/rooms", `{"display_name":"First"}`, key, nil)
	if first.status != 201 {
		t.Fatal(first.body)
	}
	limited := f.request(t, client, "POST", "/api/v1/rooms", `{"display_name":"First"}`, key, nil)
	errorCode(t, limited, 429, "RATE_LIMITED")
	clock.advance(time.Second)
	replayed := f.request(t, client, "POST", "/api/v1/rooms", `{"display_name":"First"}`, key, nil)
	if replayed.status != 201 || replayed.body != first.body || replayed.header.Get("Idempotency-Replayed") != "true" {
		t.Fatal("rate rejection damaged the retry record")
	}
}

func TestServerConfigurationAndShutdown(t *testing.T) {
	for _, config := range []Config{
		{}, {Origin: "https://example.test/path"}, {Origin: "https://user@example.test"},
		{Origin: "ftp://example.test"}, {Origin: "https://example.test", AllowInsecureCookies: true},
		{Origin: "http://example.test", AllowInsecureCookies: true},
		{Origin: "https://example.test", Limits: Limits{MaxSessions: -1}},
		{Origin: "https://example.test", Limits: Limits{MutationRate: math.NaN()}},
		{Origin: "https://example.test", Limits: Limits{BootstrapRate: math.Inf(1)}},
	} {
		if app, err := NewServer(config); err == nil {
			app.Close()
			t.Fatal("invalid configuration accepted")
		}
	}
	f := newFixture(t, Config{}, false)
	client, _ := f.actor(t)
	f.app.Close()
	f.app.Close()
	errorCode(t, f.request(t, client, "POST", "/api/v1/rooms", `{"display_name":"Alice"}`, f.key(), nil), 503, "SHUTTING_DOWN")
}

type unavailableEntropy struct{}

func (unavailableEntropy) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }

func TestSessionEntropyFailureIsPrivateAndAtomic(t *testing.T) {
	f := newFixture(t, Config{}, false)
	f.app.entropy = unavailableEntropy{}
	result := f.request(t, f.client(t), "POST", "/api/v1/session", "{}", "", nil)
	errorCode(t, result, 500, "INTERNAL")
	if len(result.cookies) != 0 || strings.Contains(result.body, "entropy") {
		t.Fatal("entropy failure leaked an internal error or issued a credential")
	}
	f.app.mu.Lock()
	defer f.app.mu.Unlock()
	if len(f.app.sessions) != 0 || len(f.app.players) != 0 {
		t.Fatal("failed credential creation left a session")
	}
}
