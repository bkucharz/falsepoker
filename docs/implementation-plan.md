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
Next-round advancement, redealing, and public withdrawal commands remain in 1.7.

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

## Phase 2 — Coordinate rooms in memory

**Result:** multiple rooms can run games safely through a Go API, with no networking.

### 2.1 — Add the room registry and lobby behavior

**Dependencies:** 0.2, Phase 1 gate.

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

**Result:** people can play the verified protocol through a usable browser UI.

Frontend tasks establish npm scripts `dev`, `build`, `typecheck`, `test` (a
noninteractive unit/component run), and `test:e2e` (Playwright). Commit the lockfile.
The browser-test task must document browser installation and use local test servers.

### 4.1 — Bootstrap React/TypeScript/Vite and the room lobby

**Dependencies:** Phase 3 gate.

**Acceptance criteria:**

- Add `web` with minimal dependencies, the declared scripts, and local development
  proxying/configuration for HTTP and WebSockets.
- Create/join via room code or invitation link, choose a name, show membership and
  readiness, and start according to the approved policy.
- Preserve anonymous session ownership without displaying or logging credentials.
- Display server validation errors and loading/connection states; duplicate clicks
  do not create duplicate participants or starts.
- Component tests cover form errors, invitation handling, and lobby permissions.

**Verification:** `npm --prefix web ci`; `npm --prefix web run typecheck`;
`npm --prefix web test`; `npm --prefix web run build`.
Manual smoke test: `go run ./cmd/server` and `npm --prefix web run dev` in separate
terminals; join the same lobby from independent browser profiles.

### 4.2 — Render gameplay and submit player actions

**Dependencies:** 4.1.

**Acceptance criteria:**

- Render private cards, public hand sizes, current turn/bid, and legal command forms.
- Submit bids/challenges and phase-advance actions; reconcile to authoritative
  snapshots rather than predicting penalties, dealing, or winners locally.
- Render revealed round results, elimination, and the terminal winner state.
- Spectators, including eliminated players, see survivors' current hands only
  while holding a peek control. Test default concealment, press/hold/release,
  touch cancellation, keyboard use, and loss of focus; active players never
  receive opponent hands. Public completed-round reveals need no peek hold.
- Surface rejected/stale commands and prevent accidental repeated submission;
  server validation remains authoritative even if client controls are bypassed.
- Component tests cover every phase, action errors, and snapshot replacement.

**Verification:** `npm --prefix web run typecheck`; `npm --prefix web test`;
`npm --prefix web run build`; `go test ./...`.

### 4.3 — Add reconnect UX and complete-browser scenarios

**Dependencies:** 4.2.

**Acceptance criteria:**

- Show reconnecting/offline/expired-room states, retry connections with bounded
  backoff, and stop retries for terminal session errors.
- Restore the server snapshot after reconnect; discard stale state and do not
  blindly replay gameplay actions outside the protocol's retry contract.
- Browser tests use isolated sessions to cover a complete game, invitation joins,
  page reload, transient disconnect, simultaneous tabs, and expiry.
- Verify reveal visibility and role-based new-round hand access, including
  eliminated and late-joining viewers using hold-to-peek, in separate browser
  sessions. Test replay with a former spectator becoming an active player.

**Verification:** `npm --prefix web run typecheck`; `npm --prefix web test`;
`npm --prefix web run test:e2e`; `npm --prefix web run build`;
`go test -race ./...`. Configure the E2E runner to start local frontend/backend
servers and document any one-time browser installation in `web`'s README.

## Phase 5 — Make the MVP reproducible and reviewable

**Result:** a contributor can run and verify the full game from a fresh checkout.

### 5.1 — Finish usability and document native local operation

**Dependencies:** 4.3.

**Acceptance criteria:**

- Support usable narrow and desktop layouts, labeled keyboard-accessible controls,
  visible focus, and understandable turn/error/disconnect messages.
- Document Go/Node versions, installation, server/frontend commands, test commands,
  configuration, and the in-memory restart limitation.
- Manually play through two independent browser sessions; include a temporary
  disconnect and verify the finished-game state.
- Keep PWA/offline gameplay, accounts, matchmaking, rankings, chat, and bots out of scope.

**Verification:** `npm --prefix web run typecheck`; `npm --prefix web test`;
`npm --prefix web run test:e2e`; `npm --prefix web run build`;
`go test -race ./...`; `go vet ./...`; `git diff --check`.
Follow the README from a clean checkout and record the manual usability checks.

### 5.2 — Add minimal continuous verification

**Dependencies:** 5.1.

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
