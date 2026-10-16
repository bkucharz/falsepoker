package transport

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkucharz/falsepoker/internal/game"
	"github.com/bkucharz/falsepoker/internal/room"
)

type fixture struct {
	server   *httptest.Server
	app      *Server
	sequence atomic.Uint64
}
type received struct {
	status  int
	body    string
	header  http.Header
	cookies []*http.Cookie
}

func newFixture(t *testing.T, config Config, tls bool) *fixture {
	t.Helper()
	server := httptest.NewUnstartedServer(nil)
	scheme := "http"
	if tls {
		scheme = "https"
	}
	config.Origin = scheme + "://" + server.Listener.Addr().String()
	config.AllowInsecureCookies = !tls
	// Most cases test behavior rather than ingress pacing. Dedicated tests use
	// small explicit budgets and a controllable clock, without sleeping.
	if config.Limits.MutationBurst == 0 {
		config.Limits.MutationBurst = 1000
	}
	if config.Limits.MutationRate == 0 {
		config.Limits.MutationRate = 1000
	}
	if config.Limits.BootstrapBurst == 0 {
		config.Limits.BootstrapBurst = 1000
	}
	if config.Limits.BootstrapRate == 0 {
		config.Limits.BootstrapRate = 1000
	}
	app, err := NewServer(config)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	server.Config.Handler = app
	if tls {
		server.StartTLS()
	} else {
		server.Start()
	}
	t.Cleanup(func() { server.Close(); app.Close() })
	return &fixture{server: server, app: app}
}

func (f *fixture) key() string { return fmt.Sprintf("%032x", f.sequence.Add(1)) }

func (f *fixture) client(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := *f.server.Client()
	client.Jar = jar
	client.Timeout = 5 * time.Second
	return &client
}

func (f *fixture) do(client *http.Client, method, path, body, key string, headers map[string]string) (received, error) {
	r, err := http.NewRequest(method, f.server.URL+path, strings.NewReader(body))
	if err != nil {
		return received{}, err
	}
	if method == "POST" {
		r.Header.Set("Origin", f.app.origin)
		r.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	for name, value := range headers {
		if value == "" {
			r.Header.Del(name)
		} else {
			r.Header.Set(name, value)
		}
	}
	response, err := client.Do(r)
	if err != nil {
		return received{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return received{}, err
	}
	return received{response.StatusCode, string(data), response.Header.Clone(), response.Cookies()}, nil
}

func (f *fixture) request(t *testing.T, client *http.Client, method, path, body, key string, headers map[string]string) received {
	t.Helper()
	result, err := f.do(client, method, path, body, key, headers)
	if err != nil {
		t.Fatal(err)
	}
	if result.header.Get("Content-Type") != "application/json" || result.header.Get("Cache-Control") != "no-store" || !json.Valid([]byte(result.body)) {
		t.Fatalf("response is not uncached JSON: status %d body %s", result.status, result.body)
	}
	return result
}

func (f *fixture) actor(t *testing.T) (*http.Client, string) {
	t.Helper()
	client := f.client(t)
	result := f.request(t, client, "POST", "/api/v1/session", "{}", "", nil)
	if result.status != 201 {
		t.Fatalf("session: %d %s", result.status, result.body)
	}
	var data struct {
		PlayerID string `json:"player_id"`
	}
	if err := json.Unmarshal([]byte(result.body), &data); err != nil {
		t.Fatal(err)
	}
	return client, data.PlayerID
}

func (f *fixture) create(t *testing.T, client *http.Client, name string) membershipResponse {
	t.Helper()
	body, err := json.Marshal(map[string]string{"display_name": name})
	if err != nil {
		t.Fatal(err)
	}
	result := f.request(t, client, "POST", "/api/v1/rooms", string(body), f.key(), nil)
	if result.status != 201 {
		t.Fatalf("create: %d %s", result.status, result.body)
	}
	var data membershipResponse
	if err := json.Unmarshal([]byte(result.body), &data); err != nil {
		t.Fatal(err)
	}
	return data
}

func errorCode(t *testing.T, result received, status int, code string) {
	t.Helper()
	var body struct {
		Error problem `json:"error"`
	}
	if err := json.Unmarshal([]byte(result.body), &body); err != nil {
		t.Fatal(err)
	}
	if result.status != status || body.Error.Code != code {
		t.Fatalf("got %d %s; want %d %s", result.status, result.body, status, code)
	}
}

func TestHTTPSessionsAndCookies(t *testing.T) {
	for _, tls := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%v", tls), func(t *testing.T) {
			f := newFixture(t, Config{}, tls)
			client := f.client(t)
			first := f.request(t, client, "POST", "/api/v1/session", "{}", "", nil)
			if first.status != 201 || len(first.cookies) != 1 {
				t.Fatal("new session did not set one cookie")
			}
			cookie := first.cookies[0]
			if cookie.Name != CookieName || !lowerHex(cookie.Value, 64) || !cookie.HttpOnly || cookie.Secure != tls || cookie.Path != "/" || cookie.Domain != "" || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
				t.Fatal("incorrect session cookie properties")
			}
			if strings.Contains(first.body, cookie.Value) {
				t.Fatal("credential leaked into JSON")
			}
			second := f.request(t, client, "POST", "/api/v1/session", "{}", "", nil)
			if second.status != 200 || second.body != first.body || len(second.cookies) != 0 {
				t.Fatal("session initialization rotated identity or credential")
			}
			_, other := f.actor(t)
			if strings.Contains(first.body, other) {
				t.Fatal("separate cookies share a public identity")
			}
			room := f.create(t, client, "Alice")
			if strings.Contains(room.RoomID, cookie.Value) || room.PlayerID == cookie.Value {
				t.Fatal("public identifiers are credentials")
			}
		})
	}
}

func TestHTTPRoomLifecycleAndAuthorization(t *testing.T) {
	f := newFixture(t, Config{}, false)
	alice, aliceID := f.actor(t)
	bob, bobID := f.actor(t)
	outsider, _ := f.actor(t)
	created := f.create(t, alice, " Alice ")
	if created.PlayerID != aliceID || created.Revision != "1" || !lowerHex(created.RoomCode, 12) || !opaqueID(created.RoomID) {
		t.Fatalf("bad membership metadata: %+v", created)
	}
	path := "/api/v1/rooms/" + created.RoomCode
	info := f.request(t, alice, "GET", path, "", "", nil)
	var data struct {
		State roomState `json:"state"`
	}
	if err := json.Unmarshal([]byte(info.body), &data); err != nil {
		t.Fatal(err)
	}
	if info.status != 200 || data.State.AdminID != aliceID || data.State.RecipientID != aliceID || data.State.Members[0].DisplayName != "Alice" || data.State.Members[0].Ready || data.State.Game != nil || data.State.GameID != "0" || data.State.Role != "spectator" {
		t.Fatalf("unexpected lobby: %s", info.body)
	}
	unauthorized := f.request(t, outsider, "GET", path, "", "", nil)
	unknown := f.request(t, outsider, "GET", "/api/v1/rooms/000000000000", "", "", nil)
	errorCode(t, unauthorized, 404, "ROOM_UNAVAILABLE")
	if unauthorized.body != unknown.body {
		t.Fatal("room existence disclosed to a nonmember")
	}
	joined := f.request(t, bob, "POST", "/"+strings.ToUpper(created.RoomCode), "{}", f.key(), nil)
	// A room code by itself is not an HTTP endpoint or a preview.
	if joined.status != 404 {
		t.Fatal("unexpected route accepted")
	}
	joined = f.request(t, bob, "POST", "/api/v1/rooms/"+strings.ToUpper(created.RoomCode)+"/join", `{"display_name":"Alice"}`, f.key(), nil)
	if joined.status != 201 || joined.header.Get("Location") != path {
		t.Fatalf("join failed: %d %s", joined.status, joined.body)
	}
	if err := json.Unmarshal([]byte(f.request(t, bob, "GET", path, "", "", nil).body), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.State.Members) != 2 || data.State.Members[1].PlayerID != bobID || data.State.Revision != "2" {
		t.Fatal("same display name merged player identities")
	}
	again := f.request(t, bob, "POST", path+"/join", `{"display_name":"Rename attempt"}`, f.key(), nil)
	if again.status != 200 {
		t.Fatalf("existing membership not idempotent: %s", again.body)
	}
	if err := json.Unmarshal([]byte(f.request(t, bob, "GET", path, "", "", nil).body), &data); err != nil {
		t.Fatal(err)
	}
	if data.State.Revision != "2" || data.State.Members[1].DisplayName != "Alice" {
		t.Fatal("repeat join renamed a player or advanced revision")
	}
	wrong := f.request(t, alice, "POST", path+"/leave", `{"room_id":"another_lifetime"}`, f.key(), nil)
	errorCode(t, wrong, 409, "STALE_ROOM")
	left := f.request(t, alice, "POST", path+"/leave", `{"room_id":"`+created.RoomID+`"}`, f.key(), nil)
	if left.status != 200 {
		t.Fatalf("leave: %s", left.body)
	}
	errorCode(t, f.request(t, alice, "GET", path, "", "", nil), 404, "ROOM_UNAVAILABLE")
	if err := json.Unmarshal([]byte(f.request(t, bob, "GET", path, "", "", nil).body), &data); err != nil {
		t.Fatal(err)
	}
	if data.State.AdminID != bobID || len(data.State.Members) != 1 || data.State.Revision != "3" {
		t.Fatal("admin did not transfer on departure")
	}
	key := f.key()
	last := f.request(t, bob, "POST", path+"/leave", `{"room_id":"`+created.RoomID+`"}`, key, nil)
	replay := f.request(t, bob, "POST", path+"/leave", `{"room_id":"`+created.RoomID+`"}`, key, nil)
	if last.status != 200 || replay.status != 200 || last.body != replay.body || replay.header.Get("Idempotency-Replayed") != "true" {
		t.Fatal("last-member leave could not be retried after room deletion")
	}
	errorCode(t, f.request(t, bob, "GET", path, "", "", nil), 404, "ROOM_UNAVAILABLE")
	errorCode(t, f.request(t, bob, "POST", path+"/leave", `{"room_id":"`+created.RoomID+`"}`, f.key(), nil), 404, "ROOM_UNAVAILABLE")
}

func TestHTTPValidation(t *testing.T) {
	f := newFixture(t, Config{}, false)
	client, _ := f.actor(t)
	tests := []struct {
		name, method, path, body string
		headers                  map[string]string
		status                   int
		code                     string
		noKey                    bool
	}{
		{"missing origin", "POST", "/api/v1/rooms", `{"display_name":"Alice"}`, map[string]string{"Origin": ""}, 403, "ORIGIN_NOT_ALLOWED", false},
		{"foreign origin", "POST", "/api/v1/rooms", `{"display_name":"Alice"}`, map[string]string{"Origin": "https://evil.example"}, 403, "ORIGIN_NOT_ALLOWED", false},
		{"null origin", "POST", "/api/v1/rooms", `{"display_name":"Alice"}`, map[string]string{"Origin": "null"}, 403, "ORIGIN_NOT_ALLOWED", false},
		{"wrong media", "POST", "/api/v1/rooms", `{"display_name":"Alice"}`, map[string]string{"Content-Type": "text/plain"}, 415, "UNSUPPORTED_MEDIA_TYPE", false},
		{"missing key", "POST", "/api/v1/rooms", `{"display_name":"Alice"}`, nil, 400, "INVALID_REQUEST", true},
		{"invalid key", "POST", "/api/v1/rooms", `{"display_name":"Alice"}`, map[string]string{"Idempotency-Key": "bad"}, 400, "INVALID_REQUEST", false},
		{"missing name", "POST", "/api/v1/rooms", `{}`, nil, 400, "INVALID_REQUEST", false},
		{"null name", "POST", "/api/v1/rooms", `{"display_name":null}`, nil, 400, "INVALID_REQUEST", false},
		{"wrong type", "POST", "/api/v1/rooms", `{"display_name":17}`, nil, 400, "INVALID_REQUEST", false},
		{"empty name", "POST", "/api/v1/rooms", `{"display_name":" "}`, nil, 400, "INVALID_MEMBER", false},
		{"long name", "POST", "/api/v1/rooms", `{"display_name":"` + strings.Repeat("a", 33) + `"}`, nil, 400, "INVALID_MEMBER", false},
		{"control name", "POST", "/api/v1/rooms", `{"display_name":"bad\u0000name"}`, nil, 400, "INVALID_MEMBER", false},
		{"duplicate key", "POST", "/api/v1/rooms", `{"display_name":"Alice","display_name":"Bob"}`, nil, 400, "INVALID_REQUEST", false},
		{"escaped duplicate", "POST", "/api/v1/rooms", `{"display_name":"Alice","\u0064isplay_name":"Bob"}`, nil, 400, "INVALID_REQUEST", false},
		{"actor injection", "POST", "/api/v1/rooms", `{"display_name":"Alice","player_id":"someone_else"}`, nil, 400, "INVALID_REQUEST", false},
		{"array", "POST", "/api/v1/rooms", `[]`, nil, 400, "INVALID_REQUEST", false},
		{"null", "POST", "/api/v1/rooms", `null`, nil, 400, "INVALID_REQUEST", false},
		{"truncated", "POST", "/api/v1/rooms", `{"display_name":`, nil, 400, "INVALID_REQUEST", false},
		{"trailing JSON", "POST", "/api/v1/rooms", `{"display_name":"Alice"}{}`, nil, 400, "INVALID_REQUEST", false},
		{"invalid UTF8", "POST", "/api/v1/rooms", "{\"display_name\":\"\xff\"}", nil, 400, "INVALID_REQUEST", false},
		{"oversized", "POST", "/api/v1/rooms", strings.Repeat(" ", maxBodyBytes+1), nil, 413, "PAYLOAD_TOO_LARGE", false},
		{"invalid room code", "GET", "/api/v1/rooms/not-a-code", "", nil, 400, "INVALID_REQUEST", true},
		{"unexpected query", "GET", "/health?player_id=other", "", nil, 400, "INVALID_REQUEST", true},
		{"GET body", "GET", "/health", "{}", nil, 400, "INVALID_REQUEST", true},
		{"unknown route", "GET", "/missing", "", nil, 404, "NOT_FOUND", true},
		{"unimplemented socket", "GET", "/api/v1/rooms/000000000000/ws", "", nil, 404, "NOT_FOUND", true},
		{"wrong method", "DELETE", "/api/v1/rooms", "", nil, 405, "METHOD_NOT_ALLOWED", true},
		{"session fields", "POST", "/api/v1/session", `{"display_name":"Alice"}`, nil, 400, "INVALID_REQUEST", true},
		{"missing leave ID", "POST", "/api/v1/rooms/000000000000/leave", `{}`, nil, 400, "INVALID_REQUEST", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := f.key()
			if tt.noKey {
				key = ""
			}
			result := f.request(t, client, tt.method, tt.path, tt.body, key, tt.headers)
			errorCode(t, result, tt.status, tt.code)
			if tt.status == 405 && result.header.Get("Allow") != "POST" {
				t.Fatal("405 omitted Allow")
			}
		})
	}
	// Validation failures did not consume any room capacity.
	created := f.create(t, client, strings.Repeat("ą", 32))
	if created.Revision != "1" {
		t.Fatal("Unicode code-point length validation failed")
	}
}

func TestHTTPInvalidCredentials(t *testing.T) {
	f := newFixture(t, Config{}, false)
	owner, _ := f.actor(t)
	created := f.create(t, owner, "Owner")
	client := f.client(t)
	for _, tt := range []struct{ name, cookie, code string }{
		{"missing", "", "AUTH_REQUIRED"},
		{"malformed", CookieName + "=bad", "SESSION_EXPIRED"},
		{"unknown", CookieName + "=" + strings.Repeat("a", 64), "SESSION_EXPIRED"},
		{"duplicates", CookieName + "=" + strings.Repeat("a", 64) + "; " + CookieName + "=" + strings.Repeat("b", 64), "SESSION_EXPIRED"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := f.request(t, client, "GET", "/api/v1/rooms/"+created.RoomCode, "", "", map[string]string{"Cookie": tt.cookie})
			errorCode(t, result, 401, tt.code)
			if strings.Contains(result.body, created.RoomID) || strings.Contains(result.body, "Owner") {
				t.Fatal("authentication error leaked room state")
			}
			if tt.cookie != "" && (len(result.cookies) != 1 || result.cookies[0].MaxAge != -1) {
				t.Fatal("invalid credential cookie was not cleared")
			}
		})
	}
}

func workers(count int, work func(int) error) []error {
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; results <- work(i) }()
	}
	close(start)
	wg.Wait()
	close(results)
	var errors []error
	for err := range results {
		if err != nil {
			errors = append(errors, err)
		}
	}
	return errors
}

func TestHTTPConcurrentJoinsRespectCapacity(t *testing.T) {
	registry, err := room.NewRegistryWithLimits(4, 24)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, Config{Registry: registry}, false)
	owner, _ := f.actor(t)
	created := f.create(t, owner, "Owner")
	clients := make([]*http.Client, 36)
	for i := range clients {
		clients[i], _ = f.actor(t)
	}
	var accepted atomic.Int32
	for _, err := range workers(len(clients), func(i int) error {
		result, err := f.do(clients[i], "POST", "/api/v1/rooms/"+created.RoomCode+"/join", `{"display_name":"Member"}`, f.key(), nil)
		if err != nil {
			return err
		}
		if result.status == 201 {
			accepted.Add(1)
			return nil
		}
		if result.status != 409 || !strings.Contains(result.body, `"code":"ROOM_FULL"`) {
			return fmt.Errorf("unexpected join: %d %s", result.status, result.body)
		}
		return nil
	}) {
		t.Fatal(err)
	}
	var data struct {
		State roomState `json:"state"`
	}
	if err := json.Unmarshal([]byte(f.request(t, owner, "GET", "/api/v1/rooms/"+created.RoomCode, "", "", nil).body), &data); err != nil {
		t.Fatal(err)
	}
	if accepted.Load() != 23 || len(data.State.Members) != 24 || data.State.Revision != "24" {
		t.Fatal("concurrent joins exceeded capacity or lost membership/revisions")
	}
	seen := map[string]bool{}
	for _, member := range data.State.Members {
		if seen[member.PlayerID] {
			t.Fatal("duplicate member")
		}
		seen[member.PlayerID] = true
	}
}

func TestHTTPConcurrentDuplicateCreationAndJoin(t *testing.T) {
	f := newFixture(t, Config{}, false)
	owner, _ := f.actor(t)
	key := f.key()
	results := make(chan received, 32)
	for _, err := range workers(32, func(int) error {
		r, e := f.do(owner, "POST", "/api/v1/rooms", `{"display_name":"Owner"}`, key, nil)
		if e == nil {
			results <- r
		}
		return e
	}) {
		t.Fatal(err)
	}
	close(results)
	var first received
	originals := 0
	for result := range results {
		if result.status != 201 {
			t.Fatalf("create: %s", result.body)
		}
		if first.body == "" {
			first = result
		}
		if result.body != first.body {
			t.Fatal("duplicate creates produced different rooms")
		}
		if result.header.Get("Idempotency-Replayed") == "" {
			originals++
		}
	}
	if originals != 1 {
		t.Fatal("duplicate creates were applied more than once")
	}
	var created membershipResponse
	if err := json.Unmarshal([]byte(first.body), &created); err != nil {
		t.Fatal(err)
	}
	bob, _ := f.actor(t)
	key = f.key()
	results = make(chan received, 32)
	for _, err := range workers(32, func(int) error {
		r, e := f.do(bob, "POST", "/api/v1/rooms/"+created.RoomCode+"/join", `{"display_name":"Bob"}`, key, nil)
		if e == nil {
			results <- r
		}
		return e
	}) {
		t.Fatal(err)
	}
	close(results)
	originals = 0
	var joinBody string
	for result := range results {
		if result.status != 201 {
			t.Fatalf("join: %s", result.body)
		}
		if joinBody == "" {
			joinBody = result.body
		}
		if result.body != joinBody {
			t.Fatal("duplicate join returned a different result")
		}
		if result.header.Get("Idempotency-Replayed") == "" {
			originals++
		}
	}
	if originals != 1 {
		t.Fatal("duplicate joins were applied more than once")
	}
	current, err := f.app.registry.Lookup(created.RoomCode)
	if err != nil {
		t.Fatal(err)
	}
	view, err := current.View(game.PlayerID(created.PlayerID))
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Members) != 2 || view.Revision != 2 {
		t.Fatal("duplicate requests changed membership/revision")
	}
}
