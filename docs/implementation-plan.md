# False Poker — MVP implementation plan

## Scope and approach

This plan follows [requirements](requirements.md), [game rules](game-rules.md),
[architecture](architecture.md), and [agent instructions](../AGENTS.md).
Each numbered task is intended to be one small pull request. Split a task further
if its implementation cannot be reviewed comfortably in one sitting. Every task
leaves a runnable, testable increment; early increments are Go packages exercised
through tests rather than a browser application.

Implement and verify the complete pure Go Game Engine before adding networking.
Prove multiplayer communication with automated clients before building the full
UI. Keep active state in memory and run locally without external services.

The workspace already contains `go.mod` and card/deck code and tests under
`internal/game`. Task 1.1 reviews and verifies that baseline rather than rebuilding
it. This plan does not change application code or resolve unspecified rules.

### Working conventions

- Dependencies refer to task IDs. Complete prerequisites before dependent work.
- Package paths below are planned conventions: `internal/game` for domain logic,
  `internal/room` for room coordination and player views, `internal/transport`
  for HTTP/WebSocket adapters, `internal/integration` for multiplayer scenarios,
  `cmd/server` for composition, and `web` for the frontend. Avoid extra layers
  unless an actual responsibility needs one.
- Commands run from the repository root unless stated otherwise. Commands for
  future packages and scripts become applicable when their task creates them.
- Each implementation PR includes the tests described by its acceptance criteria.
  Use table-driven domain tests and seeded randomness. Inject controllable time
  for lifecycle tests; avoid correctness assertions based on arbitrary sleeps.
- Run the task's focused checks and `go test ./...` for every Go PR. Run
  `go vet ./...` when changing Go code. Use the race detector for concurrent code.
- A passing command alone is insufficient: check that the stated scenarios are
  covered. Documentation decisions require human review, not just whitespace checks.
- No task authorizes silently choosing an unresolved gameplay rule. Decision gates
  block only the work that depends on them; independent work may continue.

## Phase 0 — Finalize behavioral contracts

**Result:** reviewed rules and lifecycle decisions that can be implemented and tested.

### 0.1 — Resolve remaining game-rule ambiguities

**Dependencies:** none.

**Status:** complete. Elimination timing/formula, player bounds, admin choices,
seat order, manual round advancement, rank parameters, flush cardinality, and
privacy are documented. An ordinary flush is any five cards of the named suit;
six matching cards also satisfy that claim. Phase 0.2 lifecycle decisions are
documented separately below.

**Work:** update the rules with explicit decisions and examples. Preserve the
documented recalculation of the elimination limit and absence of suit precedence.

**Acceptance criteria:**

- Preserve the approved 2–24 player range and use the exclusive elimination
  threshold `min(6, floor(24 / playerCount) + 1)`. With 13–24 survivors the limit
  is two, so losing with a one-card hand eliminates a player. Verify deck capacity
  using `playerCount * (eliminationLimit - 1) <= 24`.
- Define precisely when the current round's limit is used to eliminate its loser
  and when the next round's limit is recalculated, including a worked example.
- Define flush cardinality, valid/distinct rank parameters, and two-pair ordering.
- Define initial starter selection, stable turn order, and who advances the reveal
  phase. Specify that physical shuffling/dealing is performed by the server.
- Align privacy wording across requirements, rules, and architecture: active
  players cannot see opponents' current hands; phase 0.2 authorizes all spectators
  to inspect survivors' hands through a hold-to-peek control.
- Record any still-open questions explicitly; dependent tasks remain gated.

**Verification:** `git diff --check`; manually walk through the documented minimum,
maximum, elimination-boundary, suit-only raise, and terminal-game examples.

### 0.2 — Define room and connection lifecycle policy

**Dependencies:** 0.1 where gameplay consequences are involved.

**Status:** complete. Requirements, rules, and architecture define ready-participant
selection, spectator joins and hold-to-peek access, admin succession only on
departure, no turn timeout, 60-second reconnect grace, withdrawn-player cards and
bids, starter succession, immediate wins on departure, multiple tabs, immediate
empty-room deletion, replay, member-only room information, and session/concurrency
policies. An eliminated admin retains the role.

**Work:** document lobby, disconnect, timeout, and recovery behavior in requirements
and architecture. Use the approved per-room mutex, with ordered immutable
notifications and nonblocking bounded connection queues.

**Acceptance criteria:**

- Define who may start, readiness requirements, host departure, late joins,
  explicit leaving, and whether eliminated players remain as viewers.
- Define disconnect grace periods and turn-timeout behavior, or explicitly decide
  there is no turn timeout. Any forfeit/penalty must be an approved gameplay rule.
- Define one active connection per player or an explicit alternative; distinguish
  stable player identity, room membership, and connection identity.
- Define anonymous session credentials, multiple-connection behavior, and
  authorization for public room information versus participant information.
- State that reconnect restores only a live in-memory room; process restart loses
  active games. Delete rooms when the last member leaves or their grace expires;
  occupied rooms do not expire by inactivity or game completion. Define replay
  and shutdown behavior.
- Document room ownership, notification ordering, bounded queues, and overflow
  behavior. Network I/O must not block room state processing.

**Verification:** `git diff --check`; review scenarios for host disconnect,
multiple tabs, abandoned rooms, and disconnect expiry concurrent with a valid move.

## Phase 1 — Complete the pure Go Game Engine

**Result:** a full game can run deterministically from initialization to a winner
in Go tests, without HTTP, WebSockets, storage, or a frontend.

### 1.1 — Verify the project and card/deck baseline

**Dependencies:** none; independent of unresolved progression rules.

**Status:** complete. Reviewed the existing dependency-free card/deck package and
added explicit two-player and twenty-four-player initial-deal cases. Verified
deterministic shuffling, card preservation, successive deals, invalid-request
atomicity, and independent hand/snapshot storage. Fresh runs of
`go test ./internal/game -cover -count=1` (100% statement coverage),
`go test ./... -count=1`, and `go vet ./...` passed.

**Acceptance criteria:**

- Retain a minimal Go module with no infrastructure dependencies in the engine.
- Deck creation yields exactly 24 unique cards: four suits and ranks 9 through A.
- Injectable randomness supports reproducible shuffles that preserve all cards.
- Dealing supports different hand sizes and successive deals without duplication.
- Invalid requests fail without partial mutation; returned hands cannot mutate the
  remaining deck or other hands.

**Verification:** `go test ./internal/game -cover`; `go vet ./...`.

### 1.2 — Model and compare bids

**Dependencies:** 0.1, 1.1.

**Status:** complete. Added all eleven bid categories, exact parameter validation,
and strict raise comparison with suit-independent ordering. Table-driven tests
cover every category pairing, primary/secondary rank ordering, equal bids, suit
changes, highest-category bids, and invalid parameters on either comparison input.
Fresh runs of `go test ./internal/game -cover -count=1` (100% statement coverage),
`go test ./... -count=1`, and `go vet ./...` passed. Claim evaluation is covered
separately by tasks 1.3 and 1.4.

**Acceptance criteria:**

- Represent all eleven documented categories and their permitted parameters.
- Reject missing, extra, out-of-range, or contradictory parameters.
- Compare categories using False Poker's hierarchy, including flush above full house.
- Compare full-house and two-pair parameters in the documented order.
- Equal bids and suit-only changes are never raises; a highest-category bid has
  no higher legal successor under the approved ordering.
- Table-driven tests cover every category boundary and parameter tie-breaker.

**Verification:** `go test ./internal/game -cover`; `go vet ./...`.

### 1.3 — Evaluate rank-based claims

**Dependencies:** 1.2.

**Status:** complete. Added `Bid.IsSatisfiedBy` for high card, pair, two pairs,
three of a kind, full house, and four of a kind over the union of dealt cards.
Evaluation validates bids and physical cards, rejects duplicates, checks the
exact claimed ranks, accepts surplus matches, and leaves input unchanged.
Table-driven tests include empty/insufficient unions and combinations split across
hands. Fresh runs of `go test ./internal/game -cover -count=1` (100% statement
coverage), `go test ./... -count=1`, and `go vet ./...` passed at completion of
this task. The same evaluator now supports sequence/suit claims through task 1.4.

**Acceptance criteria:**

- Evaluate high card, pair, two pairs, three of a kind, full house, and four of a
  kind against the union of dealt cards.
- Evaluate the exact claimed ranks; unrelated stronger combinations do not
  substitute for the claim.
- Table-driven tests cover satisfied and unsatisfied claims, insufficient cards,
  surplus matching cards, distinct-rank constraints, and cards split across hands.

**Verification:** `go test ./internal/game -cover`; `go vet ./...`.

### 1.4 — Evaluate sequence- and suit-based claims

**Dependencies:** 1.2, 1.3.

**Status:** complete. Extended `Bid.IsSatisfiedBy` to low/high straights, ordinary
flushes, straight flushes, and royal straight flushes; removed the temporary
unsupported-category error. Shared rank/suit counts check exact sequences and
named suits, while any five of a named suit satisfy an ordinary flush.
Table-driven tests cover all suits, every missing sequence rank, wrong-suit
substitutions, surplus cards, both straights in one union, all six suited cards,
empty unions, invalid input, and card-order independence. Fresh runs of
`go test ./internal/game -cover -count=1` (98.8% statement coverage),
`go test ./... -count=1`, and `go vet ./...` passed.

**Acceptance criteria:**

- Evaluate low straight 9–K, high straight 10–A, flush, straight flush 9–K,
  and royal straight flush 10–A using the approved cardinality rules.
- Suit-specific claims require the named suit; missing ranks cannot be replaced
  by duplicate ranks or ranks from another suit in a straight flush.
- Table-driven tests cover all suits, missing-card boundaries, surplus cards,
  and both straights present in the same union.

**Verification:** `go test ./internal/game -cover`; `go vet ./...`.

### 1.5 — Initialize rounds and enforce bidding turns

**Dependencies:** 0.1, 1.2.

**Status:** complete. Added private game state, stable participant IDs and seat
order, explicit game/round phases, round-one initialization with injected
randomness, and the approved elimination limit for every supported player count.
`PlaceBid` records opening bids/strict raises and advances past eliminated seats;
`ValidateChallenge` checks eligibility without resolving a challenge. Copied
snapshots expose full state only to trusted domain callers. Table-driven tests
cover configuration errors, turn order/wraparound, eliminated/unknown actors,
legal bluffs, invalid/equal/lower/suit-only raises, challenge eligibility, snapshot
ownership, and rejection without state mutation. Fresh runs of
`go test ./internal/game -cover -count=1` (97.5% statement coverage),
`go test ./... -count=1`, and `go vet ./...` passed. Challenge resolution and
round progression are covered separately in tasks 1.6–1.7; room/admin/readiness policies remain
outside the engine.

**Acceptance criteria:**

- Initialize stable player identities/order, required hand sizes, current limit,
  starter, round identifier, and explicit game/round phases.
- Deal the initial round using injected randomness and the approved player bounds.
- Accept a valid opening bid and subsequent strict raises from the current player;
  advance to the next surviving player.
- Reject out-of-turn actions, unknown/eliminated players, invalid raises, and
  challenges before an opening bid without changing state.

**Verification:** `go test ./internal/game -cover`; `go vet ./...`.

### 1.6 — Resolve challenges and retain the round result

**Dependencies:** 1.3, 1.4, 1.5.

**Status:** complete. Added `Game.Challenge` to evaluate only the preceding bid
against the current dealt union, retaining per-player revealed hands, challenger,
bidder/claim, outcome, loser, round ID, and the completed round's limit. Resolution
applies one penalty or elimination, respects withdrawn bidders without additional
penalties, and identifies an immediate winner after terminal elimination. Copied
results protect authoritative state. Tests cover true/false claims, all categories
across hands, undealt-card exclusion, current-limit boundaries, withdrawn cards,
repeat/out-of-turn/inactive challenges, terminal games, evaluation-error atomicity,
and result ownership. Fresh runs of `go test ./internal/game -cover -count=1`
(97.6% statement coverage), `go test ./... -count=1`, and `go vet ./...` passed.
Next-round advancement, redealing, and public withdrawal commands are covered in 1.7.

**Acceptance criteria:**

- A valid challenge evaluates only the immediately preceding bid and the current
  round's dealt cards.
- A true claim penalizes the challenger; a false claim penalizes the bidder,
  unless that bidder already withdrew and therefore receives no additional penalty.
- Record the revealed cards, challenged bid, outcome, and losing player in a
  completed-round result; enter the agreed resolution phase.
- Resolve a round at most once. Reject repeat challenges and bidding during
  resolution without applying another penalty.

**Verification:** `go test ./internal/game -cover`; `go vet ./...`.

### 1.7 — Advance rounds, eliminate players, and finish games

**Dependencies:** 0.1, 0.2, 1.6.

**Status:** complete. Added `Game.NextRound` with designated-starter authorization,
full-deck shuffling/redealing using injected randomness, survivor-only hands,
round/limit updates, cleared bidding state, and preservation of the last result.
`GameState.NextStarter` identifies who may advance a resolution. `Game.Withdraw`
retains current cards and bids, skips departed seats, transfers pending starter
responsibility, excludes departures from future deals, and finishes immediately
when one participant remains. Rejected actions preserve state. Deterministic
complete-game tests cover player-count/limit boundaries from two to twenty-four
participants, penalties/eliminations, card conservation, hand sizes, turn order,
and terminal-action rejection. Additional tests cover true/false withdrawn bids,
limit recalculation, successive starter departures, and randomness injection.
Fresh runs of `go test ./internal/game -cover -count=1` (97.7% statement coverage),
`go test ./... -count=1`, and `go vet ./...` passed.

**Acceptance criteria:**

- Apply the approved penalty to the round loser; an already withdrawn loser
  receives no additional penalty.
- Recalculate the next round's limit at the documented point; reshuffle and
  redeal without retaining previous card identities as player possessions.
- The surviving loser starts next; if eliminated, the next survivor in turn order
  starts. Skip eliminated players in dealing and turns.
- Stop when one survivor remains; declare the winner and reject further play.
- Accept explicit withdrawal commands independently of networking. Retain departed
  players' cards and last bid through resolution, skip their turns/future deals,
  transfer next-round starter responsibility, and finish immediately when a
  departure leaves one participant. Test a true and false departed-player bid.
- Deterministic multi-round tests reach a winner and check deck capacity, card
  uniqueness, hand sizes, turn order, and state preservation after invalid commands.
- Cover supported player-count boundaries and a limit change after elimination.

**Verification:** `go test ./internal/game -cover`; `go test ./...`; `go vet ./...`.

**Phase gate:** all engine tasks pass and a complete scripted game reaches a winner
before any networking implementation begins.

**Gate status:** passed. Tasks 1.1–1.7 are complete; scripted games reach a winner
through the pure Go API without networking, storage, timers, or a frontend.

## Phase 2 — Coordinate rooms in memory

**Result:** multiple rooms can run games safely through a Go API, with no networking.

### 2.1 — Add the room registry and lobby behavior

**Dependencies:** 0.2, Phase 1 gate.

**Status:** complete. Added `internal/room` with collision-checked invitation codes,
registry lookup/deletion, stable caller-supplied member identities, display names,
readiness, admin-only starts, explicit/random seating and first-bidder selection,
spectator joins, departure handling, and replay with a new game identity. Rooms
own separate mutexes, games, and random generators; member-only lobby snapshots
are detached and contain no cards or credentials. The 24-participant limit does
not limit spectators. Tests cover code/source failures, invalid/duplicate members,
start validation/atomicity, nonparticipating admins, elimination/admin succession,
readiness reset/replay, closed references, isolation, and concurrent creation,
lookup, joins, readiness, starts, and final-leave/join races. Fresh runs of
`go test -race ./internal/room/... -cover -count=1` (97.4% statement coverage),
`go test ./... -count=1`, and `go vet ./...` passed. Participant-specific gameplay
views, gameplay command forwarding/notifications, and connection timers remain
in tasks 2.2, 2.3, and 3.4 respectively; no networking or storage was added.

**Acceptance criteria:**

- Create rooms with collision-checked invitation codes, join with a display name,
  maintain stable seats, and record readiness through an in-process API.
- Enforce the approved capacity, late-join, start, host, and leave policies.
- Test a nonparticipating admin starting with ready members, late spectator joins,
  admin retention on elimination, random admin succession on departure, and
  starting a fresh game after members declare readiness again.
- The registry owns room lookup/lifecycle; each room owns its mutable state.
- Independent rooms cannot share membership, deck, randomness, or game state.
- Concurrent create/join/read operations respect capacity and identity invariants.

**Verification:** `go test -race ./internal/room/...`; `go test ./...`; `go vet ./...`.

### 2.2 — Produce participant-specific snapshots

**Dependencies:** 2.1.

**Status:** complete. Added `Room.View` with explicit `RoomView`, `GameView`, and
`PlayerView` types, retaining the lobby-only `Room.Snapshot` API. Membership checks,
role selection, and view construction share the room mutex. Active participants
receive only their own bidding hands; spectators, including eliminated players,
receive survivors' hands. Resolution reveals all dealt cards, including departed
players' cards, and completed-round results remain public after redealing. Views
include game/round identity, phases, fixed seats, dealt counts versus required hand
sizes, turn/starter, last bid/result, and winner; they exclude the undealt deck and
credentials. Roles are recomputed for each view and new game. Mutable view data is
detached from the engine and other recipients. Table-driven and concurrent tests
cover lobby/access errors, bidding, resolution, redealing, elimination, challenge
and departure wins, admin-independent visibility, replay access reset, serialized
card privacy, and ownership. Fresh runs of
`go test -race ./internal/room/... -cover -count=1` (97.8% statement coverage),
`go test ./... -count=1`, and `go vet ./...` passed. Gameplay forwarding and
notifications remain in 2.3; the spectator hold-to-peek control belongs to the UI.

**Acceptance criteria:**

- Explicit view types expose public room/game state and the recipient's authorized
  hands according to role. All spectators may inspect all survivors' current
  hands; active players receive only their own. Never expose the undealt deck or
  credentials. Reset role-based access when a spectator participates in a new game.
- Reveal completed-round cards and preserve role-based new-round hand visibility.
- Snapshots contain enough state for a fresh client to render the current phase,
  turn, bid, hand counts, latest result, and winner without replaying old messages.
- Returned snapshots own their mutable data and cannot modify authoritative state.
- Tests inspect views for multiple recipients, eliminated players, and each phase.

**Verification:** `go test -race ./internal/room/...`; `go test ./...`; `go vet ./...`.

### 2.3 — Serialize commands and order notifications

**Dependencies:** 2.2.

**Status:** complete. Added room-level bid, challenge, and next-round commands
with game/round context validation. The existing room mutex encloses validation,
mutation, readiness reset on game completion, revision assignment, and publication.
Lobby changes and game starts use the same revision/publication path. Revisions
start at 1 and continue across replays; errors and unchanged readiness do not
advance them. Member-only subscriptions atomically queue an initial view and
receive independently owned recipient views in revision order. Each connection
has a bounded 16-view queue; overflow closes only that subscription, discards its
backlog, and signals the future transport to close its socket. Close is idempotent;
departures/deletion clean up subscriptions without treating a closed connection as
an immediate withdrawal. Earlier phase tests now drive gameplay through the room
API. Tests cover complete-game/replay behavior, stale context, errors without
mutation/notification, simultaneous challenges/advancements, conflicting commands,
concurrent views and subscription setup/closure, ordered revisions, snapshot
ownership/privacy, role changes, slow subscribers, and independent-room progress.
Fresh runs of `go test -race ./internal/room/... -cover -count=1`
(98.4% statement coverage), `go test ./... -count=1`, and `go vet ./...` passed.
No networking or storage was added; socket I/O, request retries, and connection
grace timers remain in phase 3.

**Acceptance criteria:**

- Start games and submit game commands through one serialized operation per room.
- Validation, mutation, revision assignment, and ordering of immutable recipient
  snapshots occur within that operation; socket I/O remains outside it.
- Bounded subscriber queues use the approved overflow policy and cannot block play.
- Concurrent challenges cannot resolve a round twice; conflicting commands are
  evaluated against the state left by the first accepted command.
- Tests cover ordered revisions, concurrent snapshot reads, slow subscribers,
  and one room continuing while another is busy.

**Verification:** `go test -race ./internal/room/...`; `go test ./...`; `go vet ./...`.

## Phase 3 — Implement and prove multiplayer communication

**Result:** automated network clients can create/join a room, complete a game,
disconnect, and recover without a browser UI.

### 3.1 — Specify the HTTP and WebSocket protocol

**Dependencies:** 0.2, 2.3.

**Status:** complete. Added [protocol.md](protocol.md) with versioned HTTP routes,
cookie/session and origin rules, membership-only room information, WebSocket
command/ack/error envelopes, explicit recipient snapshot/bid/card schemas, and
full-snapshot synchronization. Commands require exact observed game/round/room
revision; even unrelated lobby changes invalidate a pending command. Duplicate
outcomes are shared across tabs, retained for five minutes in bounded caches,
and checked before stale-context validation. HTTP leave retries survive room
deletion within that window. The contract specifies operational/message limits,
heartbeat/write deadlines, close reasons, 60-second initial-attachment/reconnect
grace, permanent departures, replay, and state loss after restart. A worked
create/join/ready/start/bid/raise/challenge/redeal/win/replay exchange and failure
table map MVP behavior to the current APIs and required phase 3 integration work.
Fenced JSON/HTTP bodies, example schemas/privacy/counters, new local links, and the
worked revisions/turns were checked; `git diff --check` passed. No application
code or networking was implemented in this task.

**Work:** write `docs/protocol.md` with request/response examples and state schemas.

**Acceptance criteria:**

- Specify room creation/join/information endpoints and session authentication.
- Specify readiness, start, bid, challenge, and any approved phase-advance commands;
  include request IDs, game/round context, revision checks, and structured errors.
- Define acknowledgment and duplicate-command behavior within a bounded retry
  window, including retries after a lost acknowledgment or reconnect.
- Define complete recipient-specific snapshots, revision scope, reconnect ordering,
  unauthorized access behavior, message limits, and simultaneous player connections.
- Include a complete successful exchange and examples of stale/out-of-turn moves.

**Verification:** `git diff --check`; manually map every MVP action and error case
to a documented exchange. No networking implementation is part of this PR.

### 3.2 — Add the server entry point, anonymous sessions, and HTTP endpoints

**Dependencies:** 3.1.

**Status:** complete. Added `cmd/server` with configurable loopback listener,
public origin, explicit local HTTP cookie mode, server timeouts, and signal-driven
shutdown. `internal/transport` uses the standard library for anonymous sessions,
HttpOnly/SameSite cookies (Secure by default), exact-origin checks, strict bounded
JSON input, structured errors, and create/join/member-info/leave HTTP endpoints.
Sessions and five-minute HTTP retry ledgers are bounded; repeated requests across
connections reuse the captured outcome without repeating mutations. Idle sessions
and expired retry records are cleaned up; capacity/ingress failures are explicit.
Room coordination now supplies lifetime IDs, atomic membership metadata, bounded
room/member capacity, and retained participant names. Explicit wire DTOs preserve
recipient hand privacy, string enums/counters, nullable fields, and public results.
Tests use ephemeral HTTP/TLS servers and controlled time to cover cookies,
authorization, malformed/spoofed input, duplicate requests, concurrent joins and
join/leave races, cache/session expiry and capacity, rate limits, and gameplay-view
privacy across phases/replay. `go test -race -count=1 -cover ./...` passed
(92.8% transport, 96.3% room, 97.7% engine coverage), as did `go vet ./...`,
`go mod tidy -diff`, formatting, and `git diff --check`. An actual
`go run ./cmd/server -dev-http` plus curl smoke test verified health, separate
sessions, create/retry, join, member read, admin transfer, final leave, and replay
of that leave after deletion. The README contains local run/curl instructions.
At completion of 3.2, HTTP memberships ended through explicit leave or restart;
3.3/3.4 add WebSockets and connection-grace timers. No database or frontend was added.

**Acceptance criteria:**

- Compose the existing domain and room packages in `cmd/server`; add a health endpoint.
- Implement create, join, and authorized room-information endpoints.
- Issue opaque anonymous credentials separate from room codes and display names;
  bind every operation to the authenticated player's membership.
- Reject malformed input, invalid credentials, invalid codes, full rooms, and
  disallowed joins without revealing private data.
- Tests use an ephemeral local HTTP server and cover concurrent joins.

**Verification:** `go test -race ./internal/transport/...`; `go test ./...`;
`go vet ./...`. Smoke test in separate terminals: `go run ./cmd/server` and
`curl --fail http://localhost:8080/health`. Document any configurable port override.

### 3.3 — Add authenticated WebSocket commands and snapshots

**Dependencies:** 3.2.

**Status:** complete. Gorilla WebSocket v1.5.3 supplies framing and connection
I/O entirely within transport. Upgrades check cookie identity, exact origin,
subprotocol, membership, room lifetime, and bounded connection capacity. Strict
commands derive actors from sessions and dispatch through one room operation
covering retry admission, context/revision checks, mutation, ordered publication,
and outcome retention. Retries across tabs/rounds/games return the original
outcome without another mutation. Each connection has a serialized writer,
bounded queues, message limits, write/read deadlines, and matching Ping/Pong
heartbeats. Explicit leave and server shutdown terminate associated sockets.
Real HTTP/TLS/WebSocket clients cover private hands, spectators, all command
actions, invalid input, turn/context/revision guards, concurrent duplicates and
conflicts, room isolation, rate/capacity limits, heartbeat renewal/expiry, and
shutdown. Room tests cover retry retention, admission without eviction, rejected
outcomes, no-ops, closed subscriptions, and atomic duplicate execution. The README
includes a browser-console readiness exercise. Grace/first-attachment expiry
remains in phase 3.4; no UI or persistence was added.
Verification passed: `go test -race ./internal/transport/... ./internal/room/...`,
`go test -race -count=1 -cover ./...`, `go test ./...`, `go vet ./...`,
`go mod tidy -diff`, formatting, and `git diff --check`.

**Acceptance criteria:**

- Authenticate upgrades, validate browser origins, and authorize room membership.
- Use a maintained WebSocket library with a documented choice; keep its types
  entirely outside the Game Engine.
- Deliver readiness/start/game commands to room coordination; derive actor identity
  from the session rather than trusting a payload player ID.
- Use one serialized connection writer, bounded outgoing queues, message limits,
  read/write deadlines, and heartbeat handling.
- Implement ordered snapshots, errors/acknowledgments, stale-command rejection,
  and bounded duplicate detection according to the protocol.
- Automated clients verify private hands, turn enforcement, invalid messages,
  duplicate retries, and isolation between rooms.

**Verification:** `go test -race ./internal/transport/... ./internal/room/...`;
`go test ./...`; `go vet ./...`.

### 3.4 — Restore sessions and manage connection/room lifetimes

**Dependencies:** 3.3.

**Status:** complete. Room coordination owns 60-second initial-attachment and
last-connection grace periods. Atomic attachment cancels expiry and queues a fresh
recipient snapshot; multiple tabs keep one membership, role, seat and hand.
Unique grace-period tokens reject stale callbacks across reconnect, later
disconnects, rejoining and reused room codes. Reads and mutations settle overdue
deadlines before authorization/admission, including when callbacks are delayed.
Expiry follows the existing serialized departure path: retain current-round
cards/bids, skip withdrawn turns, transfer admin/next starter, award a winner when
one participant remains, and delete an empty room. Occupied lobbies and finished
rooms have no inactivity expiry. The shared clock controls both elapsed time and
callback dispatch in tests. Shutdown cancels and joins timers, closes sockets,
and discards room/session/retry state. Tests cover exact deadline boundaries,
expiry racing with reconnect/moves, stale/early/dispatched callbacks, slow tabs,
unchanged private hands and previous-round reveals on reconnect, new spectator
memberships after expiry, released room capacity, credential expiry, and state
loss after restart. The README documents attachment deadlines and manual reconnect.
No UI, storage, additional dependency, or turn timeout was added.
Verification passed: five repeated race-enabled room/transport runs,
`go test -race -count=1 -cover ./...`, `go test ./...`, `go vet ./...`,
`go mod tidy -diff`, formatting, and `git diff --check`.

**Acceptance criteria:**

- Reconnect restores the same player and hand without rejoining or resetting a game.
- Snapshot creation and subscription are serialized so reconnect cannot miss an
  intervening update; retain the latest completed-round result as specified.
- Support simultaneous tabs for one player. Closing one must not mark the player
  disconnected while another remains; all tabs receive the same role-based view.
- Apply approved grace/timeout/leave policies through serialized room operations;
  stale timers cannot affect reconnected players or later turns.
- Delete rooms immediately when the last member leaves or their reconnect grace
  expires, releasing timers, queues, and goroutines. Occupied lobbies and finished
  rooms remain available; server shutdown closes active connections cleanly.
- Tests use controllable time and cover expiry racing with reconnect or a move,
  slow clients, expired credentials/rooms, and unavailable state after restart.

**Verification:** `go test -race ./internal/room/... ./internal/transport/...`;
`go test ./...`; `go vet ./...`.

### 3.5 — Prove complete games with headless multiplayer clients

**Dependencies:** 3.4.

**Status:** complete. Added an external-package network harness in
`internal/integration` with ephemeral HTTP/WebSocket servers, anonymous cookie
jars, client-owned wire types, bounded reader queues, controlled lifecycle time,
and seed-42 room randomness. All actions use the real protocol; the harness does
not call engine evaluation or mutate/read private room state. Scenarios complete
games and replay with explicit/random seats and starters, opening/raised bids,
independently known true/false challenges, reveal, penalties, elimination and
winners. They cover former spectators/eliminated players returning as private-hand
participants, retained reveals after redeal/reconnect, lost challenge outcomes
retried with the original context, concurrent duplicate/conflicting tabs, cached
rejections, key conflicts and per-player/per-room retry scope, unauthorized rooms,
expiry followed by spectator rejoin, and independent shuffle streams/state in
two rooms. Every incoming payload is inspected; recipient snapshots are compared
with an authorized observer at each committed revision. Adversarial fixtures
prove those privacy checks reject exposed hands, undealt cards, credentials and
incorrect card values. The only application seam is a registry seed-source
constructor; default server randomness and all game behavior remain unchanged.
README commands run the scenarios without a browser or separately started server.
Verification passed: five repeated race-enabled integration runs,
`go test -v ./internal/integration/... -count=1`, `go test -race ./...`,
`go test -race -count=1 -cover ./...`, `go vet ./...`, `go mod tidy -diff`,
formatting, and `git diff --check`. The phase 3 gate is satisfied.

**Acceptance criteria:**

- Add a reusable network test harness under `internal/integration`; start the
  server on an ephemeral port with deterministic test randomness.
- Exercise create/join, readiness/start, bids, true/false challenges, reveal,
  penalties, elimination, subsequent rounds, and a winner through the real protocol.
- Exercise disconnect/reconnect, lost acknowledgments/retries, conflicting
  commands, and at least two independent rooms.
- Inspect every recipient's network payload for privacy violations across phases.
- No browser, database, external service, or manual client is needed to reproduce tests.

**Verification:** `go test -race ./internal/integration/...`; `go test -race ./...`;
`go vet ./...`.

**Phase gate:** a complete networked game and reconnect scenario pass before the
full UI begins. Debugging multiplayer behavior must not depend on browser screens.

## Phase 4 — Build the browser client incrementally

**Result:** people can play the verified protocol through an appealing, accessible
browser UI designed first for mobile phones and usable on desktop.

Frontend tasks establish npm scripts `dev`, `build`, `typecheck`, `test` (a
noninteractive unit/component run), and `test:e2e` (Playwright). Commit the lockfile.
Browser tests document browser installation and use local test servers. Add
frontend CI with the first frontend PR; add browser CI with the first browser
smoke test, then expand its scenarios in 4.3.

**Approved UI decisions:**

- Use English and a minimal responsive poker-table screen. Keep active play in
  one viewport with no page scrolling: opponents sit around a felt table, the
  player's face-up hand stays at the bottom, primary actions sit above it, and
  current game information stays in the table center. Put secondary controls in
  a settings dialog. Scale the table for phones and desktop; never require hover.
- Establish attractive, coherent mobile screens from the start: consistent
  typography, spacing, colors, readable cards, clear action hierarchy, and obvious
  loading, success, and error feedback. Visual quality is part of phase 4
  acceptance, rather than deferred to phase 5.
- Use guided bid entry with category and applicable rank/suit fields.
- Spectators have both a hold-to-peek-all control and individual player hold
  controls. These reveal temporarily, never as persistent toggles.
- Confirm explicit leave for active participants. Explain withdrawal and its
  effect on their other tabs in that room. Reload/tab closure uses reconnect
  grace and must not submit a leave command.
- Show only the current user's connection status. Do not infer others' presence
  or show authoritative grace countdowns: the protocol exposes neither.
- Provide labeled controls, visible keyboard focus, sufficient contrast, text
  alternatives for cards, and touch targets of at least 44 by 44 CSS pixels.
  Suits and state must be understandable without relying on color alone.
- Check representative phone widths starting at 320 CSS pixels and desktop
  widths. Active play fits one viewport without scrolling. Core controls require
  no hover and work with the software keyboard. Large non-game lobby rosters may
  scroll vertically.

### 4.1 — Bootstrap React/TypeScript/Vite and the room lobby

**Status:** implemented across 4.1a–4.1c. Added `web` with React/TypeScript/Vite,
a mobile-first visual foundation, create/join/invitation forms, live member and
readiness views, independent admin seat/starter configuration, and confirmed
participant leave. The protocol client owns coordinated session bootstrap,
reload/reconnect restoration, terminal-error recovery, precise snapshot ordering,
and bounded original-intent retries without client-authoritative game decisions.
A non-secret expiry marker prevents another tab silently replacing an expired
session after its cookie is cleared; credentials stay in HttpOnly cookies.
Separate frontend and browser CI jobs use locked dependencies and local servers.
See [web/README.md](../web/README.md) for operation, tests, and design decisions.
Gameplay controls and peeking are implemented in 4.2; full-game browser recovery
and grace expiry remain in 4.3. Phone/desktop viewport screenshots have been inspected;
physical-phone keyboard/touch checks and a successful GitHub Actions run remain
manual verification on the next PR.

Local verification passed: `npm --prefix web ci`, `npm --prefix web run typecheck`,
`npm --prefix web test` (42 unit/component cases), `npm --prefix web run build`,
`npm --prefix web run format:check`, and `npm --prefix web run test:e2e` (five
Chromium scenarios covering real HTTP/WebSockets, shared tabs, reconnect, lobby
actions, responsive layouts, and explicit expired-session recovery). Also passed
`go test -race ./...`, `go vet ./...`, and `git diff --check`.

Deliver 4.1a, 4.1b, and 4.1c as separate reviewable PRs. Basic session recovery and
connection correctness belong here; 4.3 completes the UX and scenario coverage.

#### 4.1a — Frontend foundation and browser integration smoke test

**Dependencies:** Phase 3 gate.

**Acceptance criteria:**

- Add `web` with minimal dependencies, the declared scripts, a committed lockfile,
  and documented Go/Node versions. Establish the approved visual foundation with
  a responsive application shell and reusable form/button/focus styles.
- Pin Vite to port 5173 with `strictPort: true`. Proxy relative `/api/v1` HTTP
  requests and WebSockets to `http://127.0.0.1:8080`, preserving the browser Origin.
  Do not rewrite WebSocket Origin or relax backend checks.
- Document the backend command below and consistent use of
  `http://localhost:5173` for the frontend. Use same-origin cookies; credentials
  never appear in client storage, URLs, or logs.
- Add a real-browser smoke test using isolated browser contexts to initialize
  sessions, create/join through HTTP, and receive initial recipient snapshots over
  native WebSockets. A test driver may exercise APIs before the lobby UI exists.
  Verify the actual cookie/proxy/origin path rather than only mocks.
- Extend CI to run `npm ci`, type checking, unit/component tests, and production
  builds on PRs alongside Go checks. Run the smoke test in a browser job with
  required browser installation and local test servers.

**Verification:** `npm --prefix web ci`; `npm --prefix web run typecheck`;
`npm --prefix web test`; `npm --prefix web run build`;
`npm --prefix web run test:e2e`. Inspect successful frontend/browser CI jobs.
Document one-time Playwright browser installation in `web/README.md`.
For local operation, run these commands in separate terminals:

```sh
go run ./cmd/server -dev-http -origin http://localhost:5173
npm --prefix web run dev
```

#### 4.1b — Anonymous session and room connection lifecycle

**Dependencies:** 4.1a.

**Acceptance criteria:**

- Implement a small protocol client separate from presentation components, with
  session initialization, authenticated room lookup, subscription, connection
  status, bounded reconnect backoff, and terminal-error handling.
- Coordinate first-time session bootstrap across tabs, for example using a
  same-origin Web Lock, and share in-flight initialization within a tab. Test
  concurrent first-time tabs so requests cannot overwrite the shared cookie.
- Restore membership by room lookup and WebSocket attachment on reload; do not
  automatically POST join or create a new identity after expiry. Show an explicit
  recovery choice when a session or membership is unavailable.
- Own one room connection per tab. Cleanup closes the socket and cancels retries
  without leaving the room. Ignore callbacks from replaced connections. Verify
  React Strict Mode setup/cleanup creates no duplicate mutations or orphaned
  sockets/timers.
- After a failed WebSocket handshake, use authenticated HTTP room lookup to help
  classify session/membership errors: browser events cannot expose the rejected
  handshake's HTTP status/body. Network failures remain retryable; terminal
  errors stop retries. Describe `ROOM_UNAVAILABLE` as unavailable room or
  membership rather than asserting deletion. Never merge a different room
  lifetime into the current state.
- Separate authoritative snapshots, pending commands, and form drafts. Each
  connection's first snapshot establishes its baseline; subsequent snapshots
  advance revision for that room lifetime. Delayed acknowledgments never replace
  newer snapshots. Compare decimal-string counters numerically without precision
  loss and retain strings on the wire.
- Assign one request ID/idempotency key per user intent. Uncertain retries within
  the protocol's bounded window reuse the exact original command, including its
  payload, ID, and context. Do not rewrite revision or generate a new ID just to
  retry. Beyond the retention window, report an uncertain outcome and require
  review rather than automatic replay. Stale rejections refresh the view and ask
  the player to reconsider. Controls wait for the initial snapshot and prevent
  accidental repeated submission.
- Unit tests cover cleanup, replaced connections, snapshot/ack ordering, large
  counters, retry limits, and terminal/transient classification. Browser tests
  cover reload restoration and simultaneous first-time tabs. Provide a minimal
  connection view so this increment can be inspected before lobby forms exist.

**Verification:** `npm --prefix web run typecheck`; `npm --prefix web test`;
`npm --prefix web run build`; `npm --prefix web run test:e2e`.

#### 4.1c — Room forms, lobby, and admin configuration

**Dependencies:** 4.1b.

**Acceptance criteria:**

- Create/join via code or invitation link, choose a name, show membership,
  readiness, admin identity, and the current user's connection state. Provide
  explicit leave with the approved confirmation behavior.
- Admin configures explicit/random seat order and first bidder, and may start
  while spectating. All ready players participate; require 2–24 ready players
  and explain invalid counts. Do not invent admin participant-subset selection.
- Offer keyboard/touch-accessible seat ordering without requiring drag-and-drop.
  Reconcile configuration when the ready roster changes; stale choices require
  review before starting. Seat order and first-bidder selection are independent.
- Display validation errors and loading states; duplicate clicks do not create
  duplicate memberships or starts. Names are rendered as text.
- Component tests cover forms, invitations, permissions, changing ready rosters,
  leave confirmation, and rejected commands. Browser tests verify two independent
  users joining, readying, configuring, and starting through the actual UI.
- Manually inspect phone and desktop layouts, including long rosters, readable
  names, software-keyboard form entry, and touch-friendly admin controls.

**Verification:** `npm --prefix web run typecheck`; `npm --prefix web test`;
`npm --prefix web run build`; `npm --prefix web run test:e2e`.
Run the documented local commands and join from independent browser profiles.

### 4.2 — Render gameplay and submit player actions

**Dependencies:** 4.1c.

**Status:** implemented. The active game uses a full-viewport responsive table
with opponents arranged around the felt, the player's hand at the bottom,
centered turn/claim/result details, and secondary controls in settings. Added
authoritative gameplay snapshots for private cards, public hand sizes, turns and
bids; guided forms submit bids, challenges, and the designated starter's
next-round action through the existing WebSocket command client. Results,
revealed cards, eliminations, and winners render from server snapshots.
Spectator peeks are held with pointer or keyboard input and clear on release,
cancellation, focus/visibility loss, or snapshot/round/role changes. Finished
games retain the lobby readiness and admin replay flow. Component coverage
exercises every bid category and action phase, visibility, permissions, and
snapshot replacement. A real-browser flow starts a game, checks spectator
hold-to-peek, bids, challenges, and advances the designated starter.
Verification passed: `npm --prefix web run typecheck`,
`npm --prefix web test` (61 cases), `npm --prefix web run build`,
`npm --prefix web run format:check`, `npm --prefix web run test:e2e` (5 browser
scenarios), and `go test ./...`.

**Acceptance criteria:**

- Render private cards, public hand sizes, current turn/bid, and guided bid forms
  with only applicable category/rank/suit fields. Explain the game's combination
  ordering and that claims concern all dealt cards. Do not restrict bluffing based
  on the player's own hand; server validation remains authoritative.
- Submit bids/challenges and phase-advance actions; reconcile to authoritative
  snapshots rather than predicting penalties, dealing, or winners locally.
- Render revealed round results, elimination, and the terminal winner state.
- Offer “Next round” only to the designated next starter; admin status does not
  override this permission. Support readiness and admin configuration for replay.
- Spectators, including eliminated players, see survivors' current hands only
  while holding a peek control. Test default concealment, press/hold/release,
  touch cancellation, keyboard use, loss of focus, page visibility changes, and
  snapshot/round/role changes for both peek-all and individual controls. Releasing
  a control ends its reveal; without any held control, current private hands are
  absent from visible content and the accessibility tree, rather than merely
  blurred. Active players never receive opponent hands. Public completed-round
  reveals remain separate and need no peek hold.
- Surface rejected/stale commands and prevent accidental repeated submission;
  server validation remains authoritative even if client controls are bypassed.
- Component tests cover every phase, action errors, and snapshot replacement.
- Inspect mobile gameplay for clear turn/action hierarchy, readable cards and
  results, large rosters, and touch operation without hover.

**Verification:** `npm --prefix web run typecheck`; `npm --prefix web test`;
`npm --prefix web run build`; `go test ./...`.

### 4.3 — Complete recovery UX and multiplayer browser scenarios

**Dependencies:** 4.2.

**Acceptance criteria:**

- Refine recovery established in 4.1b: clear reconnecting/offline/unavailable
  session-or-room messages, explicit recovery choices, and disabled actions while
  awaiting authoritative state. Show only the user's connection status without
  unsupported presence badges or expiry countdowns.
- Verify reconnect snapshots, replaced-connection isolation, stale state rejection,
  and exact original-command retries after a lost outcome throughout gameplay.
- Browser tests use isolated sessions to cover a complete game, invitation joins,
  page reload, transient disconnect, simultaneous tabs, and expiry.
- Verify actual WebSocket loss in disconnect tests; toggling HTTP connectivity
  alone is insufficient evidence that the socket disconnected.
- Use one real 60-second server-grace expiry scenario with a suitable test timeout.
  Browser clock overrides cannot advance the Go server's clock. Keep exact expiry
  boundaries/races in existing controlled-clock Go tests; do not add production
  clock-control endpoints or repeat slow expiry waits for every UI case.
- Verify reveal visibility and role-based new-round hand access, including
  eliminated and late-joining viewers using hold-to-peek, in separate browser
  sessions. Test replay with a former spectator becoming an active player.
- Exercise both peek controls with touch and keyboard, leave confirmation, and
  phone/desktop viewports. Manually check a phone-sized viewport through a complete
  game for visual hierarchy, clarity, and comfort, beyond initial-lobby screenshots.
- Expand existing browser CI to these scenarios and retain traces or screenshots
  on failure. Tests require no external services or credentials.

**Verification:** `npm --prefix web run typecheck`; `npm --prefix web test`;
`npm --prefix web run test:e2e`; `npm --prefix web run build`;
`go test -race ./...`. Configure the E2E runner to start local frontend/backend
servers and document any one-time browser installation in `web`'s README.

## Phase 5 — Make the MVP reproducible and reviewable

**Result:** a contributor can run and verify the full game from a fresh checkout.

### 5.1 — Finish usability and document native local operation

**Dependencies:** 4.3.

**Acceptance criteria:**

- Refine and audit the mobile/desktop visual quality, accessibility, and feedback
  already required in phase 4. Check real-phone touch and software-keyboard use
  where available; fix usability issues found during complete-game playthroughs.
- Document Go/Node versions, installation, server/frontend commands, test commands,
  configuration, and the in-memory restart limitation.
- Manually play through two independent browser sessions; include a temporary
  disconnect and verify the finished-game state.
- Keep PWA/offline gameplay, accounts, matchmaking, rankings, chat, and bots out of scope.

**Verification:** `npm --prefix web run typecheck`; `npm --prefix web test`;
`npm --prefix web run test:e2e`; `npm --prefix web run build`;
`go test -race ./...`; `go vet ./...`; `git diff --check`.
Follow the README from a clean checkout and record the manual usability checks.

### 5.2 — Audit continuous verification and contributor reproducibility

**Dependencies:** 5.1.

Basic Go CI is introduced early in `.github/workflows/go-checks.yml`; see
[ci.md](ci.md). It requires the initialized Go module on every run. Frontend and
browser checks are introduced in 4.1a and expanded in 4.3; this task audits the
complete workflow rather than postponing their introduction.

**Acceptance criteria:**

- A repository CI workflow runs the established Go and frontend checks on PRs.
- Pin the documented toolchain versions; install frontend dependencies from the
  lockfile and browser dependencies only for browser-test jobs.
- All tests run without cloud credentials, production services, or persistent data.
- Preserve failure output/artifacts useful for reviewing multiplayer regressions;
  no automatic production deployment is introduced.

**Verification:** run the same commands listed in 5.1 locally; inspect a successful
CI run for the PR, including the multiplayer and browser tests.

## MVP exit criteria and deferred work

The MVP is complete when two or more supported players can create/join a private
room without registration, choose names, ready/start, receive private cards,
bid/challenge, see authorized round reveals, progress through elimination, and
reach a winner. Temporary disconnects recover according to the documented policy.
Invalid/out-of-turn actions are rejected, hidden cards are protected, and the
application runs locally without external services. Phase 5 verification passes.

Docker Compose and hosted HTTPS/WSS remain the intended delivery approach from
the architecture document, but are not prerequisites for proving the local MVP.
When a concrete hosting/demo need arises, add a separate packaging PR containing
only the frontend/server build, single-backend configuration, and necessary proxy
settings. Acceptance: a clean build starts locally, serves the UI and health check,
and supports a complete game plus reconnect through the proxy. Verification:
`docker compose config`, `docker compose build`, `docker compose up`, a health
request to the documented exposed address, and the multiplayer browser scenario
against that address. Use separate terminals and stop the stack after verification.

PostgreSQL, durable game recovery, multiple backend instances, brokers, distributed
locks, generic repository abstractions, and event sourcing are deferred. Revisit
them only against an explicit requirement; recording historical results and
recovering live games require different persistence designs.
