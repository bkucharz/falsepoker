package transport

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/bkucharz/falsepoker/internal/room"
)

type failure struct {
	status        int
	code, message string
	delay         int
}

func fail(status int, code, message string) *failure {
	return &failure{status: status, code: code, message: message}
}
func transient(status int, code, message string, delay int) *failure {
	return &failure{status: status, code: code, message: message, delay: delay}
}
func internal() *failure {
	return fail(500, "INTERNAL", "The server could not complete the operation.")
}
func busy() *failure { return transient(503, "SERVER_BUSY", "Server capacity is unavailable.", 1) }

type problem struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}
type response struct {
	status     int
	body       []byte
	location   string
	retryAfter int
	replayed   bool
}

func jsonResponse(status int, value any) response {
	data, err := json.Marshal(value)
	if err != nil || len(data) > maxOutputBytes {
		return errorResponse("", internal())
	}
	return response{status: status, body: data}
}

func errorResponse(key string, f *failure) response {
	var id *string
	if key != "" {
		id = &key
	}
	data, err := json.Marshal(struct {
		RequestID *string `json:"request_id"`
		Error     problem `json:"error"`
	}{id, problem{Code: f.code, Message: f.message, Retryable: f.delay > 0}})
	if err != nil {
		return response{status: 500, body: []byte(`{"request_id":null,"error":{"code":"INTERNAL","message":"Response encoding failed.","retryable":false}}`)}
	}
	return response{status: f.status, body: data, retryAfter: f.delay}
}

func writeResponse(w http.ResponseWriter, result response) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if result.location != "" {
		w.Header().Set("Location", result.location)
	}
	if result.retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(result.retryAfter))
	}
	if result.replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	w.WriteHeader(result.status)
	if _, err := w.Write(result.body); err != nil {
		return
	} // Delivery failure is recovered through the retry ledger.
}

func roomFailure(err error) *failure {
	switch {
	case errors.Is(err, room.ErrRoomNotFound), errors.Is(err, room.ErrRoomClosed), errors.Is(err, room.ErrNotMember):
		return fail(404, "ROOM_UNAVAILABLE", "The room is unavailable.")
	case errors.Is(err, room.ErrStaleRoom):
		return fail(409, "STALE_ROOM", "The room lifetime has changed.")
	case errors.Is(err, room.ErrRoomFull):
		return fail(409, "ROOM_FULL", "The room has reached its member capacity.")
	case errors.Is(err, room.ErrRegistryFull), errors.Is(err, room.ErrCodeUnavailable):
		return busy()
	case errors.Is(err, room.ErrInvalidMember):
		return fail(400, "INVALID_MEMBER", "Use a trimmed display name of 1–32 Unicode code points without control characters.")
	default:
		return internal()
	}
}

// ServeHTTP never implicitly joins a member through an upgrade or read route.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.closing.Load() {
		writeResponse(w, errorResponse("", transient(503, "SHUTTING_DOWN", "The server is shutting down.", 1)))
		return
	}
	route, code, method := routeFor(r.URL.Path)
	if route == "" {
		writeResponse(w, errorResponse("", fail(404, "NOT_FOUND", "The route does not exist.")))
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		writeResponse(w, errorResponse("", fail(405, "METHOD_NOT_ALLOWED", "The method is not supported.")))
		return
	}
	if (route != "ws" && r.URL.RawQuery != "") || (r.URL.RawPath != "" && strings.Contains(strings.ToLower(r.URL.RawPath), "%2f")) {
		writeResponse(w, errorResponse("", fail(400, "INVALID_REQUEST", "Unexpected URL parameters or path encoding.")))
		return
	}
	if method == http.MethodPost || route == "ws" {
		origins := r.Header.Values("Origin")
		if len(origins) != 1 || origins[0] != s.origin {
			writeResponse(w, errorResponse("", fail(403, "ORIGIN_NOT_ALLOWED", "The origin is not allowed.")))
			return
		}
	} else {
		data, problem := readBody(w, r)
		if problem != nil {
			writeResponse(w, errorResponse("", problem))
			return
		}
		if len(data) != 0 {
			writeResponse(w, errorResponse("", fail(400, "INVALID_REQUEST", "This route accepts no body.")))
			return
		}
	}
	if route == "health" {
		writeResponse(w, jsonResponse(200, map[string]string{"status": "ok"}))
		return
	}
	if route == "session" {
		s.sessionEndpoint(w, r)
		return
	}
	sess, problem := s.authenticate(r, w)
	if problem != nil {
		writeResponse(w, errorResponse("", problem))
		return
	}
	if code != "" && !lowerHex(code, 12) {
		writeResponse(w, errorResponse("", fail(400, "INVALID_REQUEST", "Use a 12-character hexadecimal room code.")))
		return
	}
	if route == "ws" {
		body, f := readBody(w, r)
		if f != nil {
			writeResponse(w, errorResponse("", f))
			return
		}
		if len(body) != 0 {
			writeResponse(w, errorResponse("", invalidJSON()))
			return
		}
		s.upgrade(w, r, sess, code)
		return
	}
	if route == "info" {
		current, err := s.registry.Lookup(code)
		if err != nil {
			writeResponse(w, errorResponse("", roomFailure(err)))
			return
		}
		view, err := current.View(sess.player)
		if err != nil {
			writeResponse(w, errorResponse("", roomFailure(err)))
			return
		}
		writeResponse(w, jsonResponse(200, struct {
			State roomState `json:"state"`
		}{toRoomState(view)}))
		return
	}
	key, problem := requestID(r)
	if problem != nil {
		writeResponse(w, errorResponse("", problem))
		return
	}
	var normalized string
	if route == "leave" {
		var body struct {
			RoomID *string `json:"room_id"`
		}
		if problem = decodeBody(w, r, &body); problem == nil {
			if body.RoomID == nil || !opaqueID(*body.RoomID) {
				problem = invalidJSON()
			} else {
				normalized = *body.RoomID
			}
		}
	} else {
		var body struct {
			DisplayName *string `json:"display_name"`
		}
		if problem = decodeBody(w, r, &body); problem == nil {
			if body.DisplayName == nil {
				problem = invalidJSON()
			} else {
				var err error
				normalized, err = room.NormalizeDisplayName(*body.DisplayName)
				if err != nil {
					problem = roomFailure(err)
				}
			}
		}
	}
	if problem != nil {
		writeResponse(w, errorResponse(key, problem))
		return
	}
	fingerprint := route + "\n" + code + "\n" + normalized
	result := s.mutate(sess, key, fingerprint, func() response { return s.membershipOperation(sess, route, code, normalized, key) })
	writeResponse(w, result)
}

func routeFor(path string) (route, code, method string) {
	switch path {
	case "/health":
		return "health", "", http.MethodGet
	case "/api/v1/session":
		return "session", "", http.MethodPost
	case "/api/v1/rooms":
		return "create", "", http.MethodPost
	}
	parts := strings.Split(path, "/")
	if (len(parts) != 5 && len(parts) != 6) || parts[0] != "" || parts[1] != "api" || parts[2] != "v1" || parts[3] != "rooms" || parts[4] == "" {
		return "", "", ""
	}
	code = strings.ToLower(parts[4])
	if len(parts) == 5 {
		return "info", code, http.MethodGet
	}
	if parts[5] == "ws" {
		return "ws", code, http.MethodGet
	}
	if parts[5] == "join" || parts[5] == "leave" {
		return parts[5], code, http.MethodPost
	}
	return "", "", ""
}

func opaqueID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

func (s *Server) sessionEndpoint(w http.ResponseWriter, r *http.Request) {
	var empty struct{}
	if problem := decodeBody(w, r, &empty); problem != nil {
		writeResponse(w, errorResponse("", problem))
		return
	}
	var sess *session
	status := http.StatusOK
	if len(r.CookiesNamed(CookieName)) == 0 {
		created, credential, problem := s.newSession(r.RemoteAddr)
		if problem != nil {
			writeResponse(w, errorResponse("", problem))
			return
		}
		sess = created
		status = http.StatusCreated
		http.SetCookie(w, &http.Cookie{Name: CookieName, Value: credential, Path: "/", HttpOnly: true, Secure: s.secureCookies, SameSite: http.SameSiteStrictMode})
	} else {
		var problem *failure
		sess, problem = s.authenticate(r, w)
		if problem != nil {
			writeResponse(w, errorResponse("", problem))
			return
		}
		sess.mu.Lock()
		ok, delay := sess.mutations.allow(s.now(), s.limits.MutationRate, s.limits.MutationBurst)
		sess.mu.Unlock()
		if !ok {
			writeResponse(w, errorResponse("", transient(429, "RATE_LIMITED", "Too many mutations.", delay)))
			return
		}
	}
	writeResponse(w, jsonResponse(status, struct {
		ProtocolVersion int    `json:"protocol_version"`
		PlayerID        string `json:"player_id"`
	}{1, string(sess.player)}))
}

type membershipResponse struct {
	RoomCode string `json:"room_code"`
	RoomID   string `json:"room_id"`
	PlayerID string `json:"player_id"`
	Revision string `json:"revision"`
}

func membershipResult(m room.Membership, status int) response {
	result := jsonResponse(status, membershipResponse{m.Code, m.RoomID, string(m.PlayerID), strconv.FormatUint(m.Revision, 10)})
	result.location = "/api/v1/rooms/" + m.Code
	return result
}

// Caller holds sess.mu. All room effects happen before their captured metadata
// is cached; writing the response happens only after every lock is released.
func (s *Server) membershipOperation(sess *session, route, code, value, key string) response {
	if route == "create" {
		if len(sess.memberships) >= s.limits.MaxMemberships {
			return errorResponse(key, fail(409, "MEMBERSHIP_LIMIT", "Too many room memberships."))
		}
		created, membership, err := s.registry.CreateWithMembership(sess.player, value)
		if err != nil {
			return errorResponse(key, roomFailure(err))
		}
		sess.memberships[membership.RoomID] = created
		return membershipResult(membership, 201)
	}
	current, err := s.registry.Lookup(code)
	if err != nil {
		return errorResponse(key, roomFailure(err))
	}
	if route == "join" {
		if sess.memberships[current.ID()] == nil && len(sess.memberships) >= s.limits.MaxMemberships {
			return errorResponse(key, fail(409, "MEMBERSHIP_LIMIT", "Too many room memberships."))
		}
		membership, created, err := current.JoinWithMembership(sess.player, value)
		if err != nil {
			return errorResponse(key, roomFailure(err))
		}
		sess.memberships[membership.RoomID] = current
		status := 200
		if created {
			status = 201
		}
		return membershipResult(membership, status)
	}
	if err := current.LeaveRoom(sess.player, value); err != nil {
		return errorResponse(key, roomFailure(err))
	}
	delete(sess.memberships, value)
	if len(sess.memberships) == 0 {
		sess.lastSeen = s.now()
	}
	return jsonResponse(200, struct {
		RoomID string `json:"room_id"`
		Left   bool   `json:"left"`
	}{value, true})
}
