// Package transport adapts authenticated HTTP requests to in-memory rooms.
// It contains no game rules. WebSocket commands are added in phase 3.3.
package transport

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bkucharz/falsepoker/internal/game"
	"github.com/bkucharz/falsepoker/internal/room"
)

const (
	CookieName     = "falsepoker_session"
	maxBodyBytes   = 8 << 10
	maxOutputBytes = 64 << 10
	sessionIdle    = 24 * time.Hour
	retryLifetime  = 5 * time.Minute
)

// Limits bounds HTTP resources; zero fields use the protocol defaults.
type Limits struct {
	MaxSessions     int
	MaxMemberships  int
	MaxRetryRecords int
	MutationRate    float64
	MutationBurst   int
	BootstrapRate   float64
	BootstrapBurst  int
}

type Config struct {
	Origin               string
	AllowInsecureCookies bool           // explicit loopback HTTP development only
	Registry             *room.Registry // nil creates a registry with default limits
	Limits               Limits
	Now                  func() time.Time // nil uses time.Now; tests may supply a controlled clock
}

// Server owns sessions and their HTTP retry ledgers. Close stops its cleanup
// goroutine; callers separately shut down the enclosing http.Server.
type Server struct {
	registry      *room.Registry
	origin        string
	secureCookies bool
	limits        Limits
	now           func() time.Time
	entropy       io.Reader
	mu            sync.Mutex
	sessions      map[[32]byte]*session
	players       map[game.PlayerID]bool
	bootstrap     map[string]*bucket
	closing       atomic.Bool
	stop          chan struct{}
	done          chan struct{}
	closeOnce     sync.Once
}

func NewServer(config Config) (*Server, error) {
	u, err := url.Parse(config.Origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("origin must be an http(s) scheme, host, and optional port without a path")
	}
	if config.AllowInsecureCookies && (u.Scheme != "http" || !loopbackHost(u.Hostname())) {
		return nil, errors.New("insecure cookies require an explicit loopback HTTP origin")
	}
	l := config.Limits
	if l.MaxSessions == 0 {
		l.MaxSessions = 1024
	}
	if l.MaxMemberships == 0 {
		l.MaxMemberships = 8
	}
	if l.MaxRetryRecords == 0 {
		l.MaxRetryRecords = 128
	}
	if l.MutationRate == 0 {
		l.MutationRate = 10
	}
	if l.MutationBurst == 0 {
		l.MutationBurst = 20
	}
	if l.BootstrapRate == 0 {
		l.BootstrapRate = 0.5
	}
	if l.BootstrapBurst == 0 {
		l.BootstrapBurst = 30
	}
	if l.MaxSessions < 1 || l.MaxMemberships < 1 || l.MaxRetryRecords < 1 || l.MutationBurst < 1 || l.BootstrapBurst < 1 || !validRate(l.MutationRate) || !validRate(l.BootstrapRate) {
		return nil, errors.New("HTTP limits must be positive and finite")
	}
	if config.Registry == nil {
		config.Registry = room.NewRegistry()
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	s := &Server{
		registry: config.Registry, origin: config.Origin, secureCookies: !config.AllowInsecureCookies,
		limits: l, now: config.Now, entropy: rand.Reader,
		sessions: make(map[[32]byte]*session), players: make(map[game.PlayerID]bool),
		bootstrap: make(map[string]*bucket), stop: make(chan struct{}), done: make(chan struct{}),
	}
	go s.cleanupLoop()
	return s, nil
}

func (s *Server) Close() {
	s.closeOnce.Do(func() { s.closing.Store(true); close(s.stop) })
	<-s.done
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validRate(rate float64) bool { return rate > 0 && !math.IsInf(rate, 0) && !math.IsNaN(rate) }

type bucket struct {
	tokens float64
	last   time.Time
}

func (b *bucket) allow(now time.Time, rate float64, burst int) (bool, int) {
	if b.last.IsZero() {
		b.tokens = float64(burst)
		b.last = now
	}
	if now.After(b.last) {
		b.tokens = math.Min(float64(burst), b.tokens+now.Sub(b.last).Seconds()*rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, max(1, int(math.Ceil((1-b.tokens)/rate)))
}

func (s *Server) cleanupLoop() {
	defer close(s.done)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			s.cleanupLocked(s.now())
			s.mu.Unlock()
		case <-s.stop:
			return
		}
	}
}

// Lock order is session-map -> individual session -> room -> registry. Room
// operations never call transport, and responses are written after unlocking.
func (s *Server) cleanupLocked(now time.Time) {
	for key, sess := range s.sessions {
		sess.mu.Lock()
		sess.prune(now)
		if !sess.active || (len(sess.memberships) == 0 && now.Sub(sess.lastSeen) >= sessionIdle) {
			sess.active = false
			delete(s.sessions, key)
			delete(s.players, sess.player)
		}
		sess.mu.Unlock()
	}
	for ip, b := range s.bootstrap {
		if now.Sub(b.last) >= retryLifetime {
			delete(s.bootstrap, ip)
		}
	}
}

func (s *Server) newSession(remote string) (*session, string, *failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.cleanupLocked(now)
	ip, _, err := net.SplitHostPort(remote)
	if err != nil {
		ip = remote
	}
	b := s.bootstrap[ip]
	if b == nil {
		if len(s.bootstrap) >= s.limits.MaxSessions {
			return nil, "", busy()
		}
		b = &bucket{}
		s.bootstrap[ip] = b
	}
	if ok, delay := b.allow(now, s.limits.BootstrapRate, s.limits.BootstrapBurst); !ok {
		return nil, "", transient(429, "RATE_LIMITED", "Too many new session requests.", delay)
	}
	if len(s.sessions) >= s.limits.MaxSessions {
		return nil, "", busy()
	}
	for attempt := 0; attempt < 16; attempt++ {
		var raw [48]byte
		if _, err := io.ReadFull(s.entropy, raw[:]); err != nil {
			return nil, "", internal()
		}
		credential := hex.EncodeToString(raw[:32])
		key := sha256.Sum256([]byte(credential))
		player := game.PlayerID("p_" + hex.EncodeToString(raw[32:]))
		if s.sessions[key] != nil || s.players[player] {
			continue
		}
		sess := &session{player: player, active: true, lastSeen: now, memberships: make(map[string]*room.Room), retries: make(map[string]retryRecord)}
		s.sessions[key] = sess
		s.players[player] = true
		return sess, credential, nil
	}
	return nil, "", internal()
}

func (s *Server) authenticate(r *http.Request, w http.ResponseWriter) (*session, *failure) {
	cookies := r.CookiesNamed(CookieName)
	if len(cookies) == 0 {
		return nil, fail(401, "AUTH_REQUIRED", "An anonymous session is required.")
	}
	if len(cookies) != 1 || !lowerHex(cookies[0].Value, 64) {
		s.clearCookie(w)
		return nil, fail(401, "SESSION_EXPIRED", "The session is unavailable.")
	}
	key := sha256.Sum256([]byte(cookies[0].Value))
	s.mu.Lock()
	sess := s.sessions[key]
	s.mu.Unlock()
	if sess == nil {
		s.clearCookie(w)
		return nil, fail(401, "SESSION_EXPIRED", "The session is unavailable.")
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	now := s.now()
	sess.prune(now)
	if !sess.active || (len(sess.memberships) == 0 && now.Sub(sess.lastSeen) >= sessionIdle) {
		sess.active = false
		s.clearCookie(w)
		return nil, fail(401, "SESSION_EXPIRED", "The session is unavailable.")
	}
	sess.lastSeen = now
	return sess, nil
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.secureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}
