# False Poker — MVP protocol v1

## Status and scope

This is the implementation contract for phases 3.2–3.5. Phases 3.2/3.3 implement
HTTP sessions/memberships and authenticated WebSocket gameplay with snapshots,
retries, limits and heartbeat handling. Phase 3.4 implements initial-attachment
and reconnect-grace timers, deadline enforcement and shutdown cleanup.
See [the README](../README.md) for local HTTP and browser-console exercises. Gameplay follows
[game-rules.md](game-rules.md); ownership and locking follow
[architecture.md](architecture.md).

HTTP creates anonymous sessions and room memberships. One WebSocket per client
connection carries room commands and complete recipient-specific snapshots.
Multiple connections may belong to one player. There are no accounts, database,
cross-server routing, incremental event replay, or client-authoritative actions.

## Encoding and identities

- HTTP routes start with `/api/v1`. JSON responses use
  `Content-Type: application/json` and `Cache-Control: no-store`.
- WebSockets negotiate the subprotocol `falsepoker.v1`. Each text message contains
  exactly one JSON object. Binary application messages are unsupported.
- Wire keys use `snake_case`; enum values are strings, independent of Go enum
  numbers. Unknown request fields, duplicate keys, trailing JSON, missing required
  fields, and incorrect types are rejected. Clients tolerate additional response
  fields but must reject unsupported protocol versions/required enum values.
- `player_id` is a server-issued public identity, not a credential. It is stable
  across tabs and rooms for one anonymous session. Display names are room-local,
  need not be unique, and never establish identity.
- `room_code` is the invitation/routing code: 12 hexadecimal characters. Inputs
  accept ASCII uppercase and normalize to lowercase. A code grants only the
  ability to request membership; it grants no pre-join preview or impersonation.
- `room_id` is a separate server-generated opaque identifier for one room lifetime.
  It changes if a deleted room's code is ever reused. It grants no authorization.
- `revision`, `game_id`, and `round_id` are canonical decimal **strings** encoding
  unsigned 64-bit integers, avoiding JavaScript integer precision loss. Leading
  zeros are invalid except for `"0"`. Revision starts at `"1"`; game/round IDs are
  `"0"` before the first game. A game ID increases on each replay within the room;
  a round ID starts at `"1"` in each game.
- IDs are opaque to clients; examples use readable player/room IDs. A `request_id`
  is 32 lowercase hexadecimal characters generated from 16 random bytes. The
  sequential request IDs below are illustrative. Never reuse one for new intent.
- Display names are trimmed, then must contain 1–32 Unicode code points, with no
  control characters. They are rendered as text, never HTML. IDs on the wire are
  at most 64 ASCII characters.

## Anonymous sessions and origin checks

`POST /api/v1/session` with `{}` creates an anonymous session when no cookie is
present. It returns `201 Created`, a public identity, and a server-generated
cookie containing at least 32 random bytes of credential entropy:

```http
POST /api/v1/session HTTP/1.1
Origin: https://falsepoker.example
Content-Type: application/json

{}
```

```http
HTTP/1.1 201 Created
Content-Type: application/json
Cache-Control: no-store
Set-Cookie: falsepoker_session=<opaque>; Path=/; HttpOnly; SameSite=Strict; Secure

{"protocol_version":1,"player_id":"p_alice"}
```

With a valid cookie, the same call returns `200 OK` and the same public identity,
without rotating its credential or creating another player. Clients initialize
the session before issuing room requests; tabs coordinate this initial bootstrap
so concurrent first-time requests do not race to overwrite a new cookie.

The cookie is host-only (no `Domain`) and has no persistent browser expiry. HTTPS
requires `Secure`; an explicitly configured local HTTP development mode may omit
that attribute. Sessions are held in memory. Unattached sessions expire after
24 hours without an authenticated request. Sessions with room memberships or live
connections are retained; removing the final membership starts the idle period.
Losing a cookie does not recover another player's identity by name or room code.

An invalid/expired cookie returns `401 SESSION_EXPIRED` and clears that cookie;
it never silently changes the actor of a protected operation. The client may
explicitly initialize a new session afterward. Missing cookies on protected
routes return `401 AUTH_REQUIRED`. Server restart loses sessions and rooms.

Every HTTP mutation, including session initialization, and every WebSocket
handshake requires `Origin` to exactly match the configured public application
origin (scheme, host, and port). Missing, `null`, and unapproved origins return
`403 ORIGIN_NOT_ALLOWED`. Headless clients supply that header too. Origin checks
complement cookie authentication; they do not authenticate a headless client.
Cross-origin credentialed CORS is not enabled. Local Vite development uses a
same-origin HTTP/WebSocket proxy and an explicitly configured development origin.

Credentials are never placed in URLs, JSON, invitation links, snapshots, or logs.
The actor always comes from the validated cookie, never from command payloads.

## HTTP endpoints

All routes except `/health` and session initialization require a valid session.
Mutation bodies require `application/json`. Room mutation requests use the
`Idempotency-Key` header described under retry behavior.

| Method and route | Request | Success |
| --- | --- | --- |
| `GET /health` | No body or authentication | `200 {"status":"ok"}`; process health only |
| `POST /api/v1/session` | `{}`; cookie optional | `201` new session or `200` existing session |
| `POST /api/v1/rooms` | `{"display_name":"Alice"}` | `201`; creator becomes sole admin and is initially unready |
| `POST /api/v1/rooms/{code}/join` | `{"display_name":"Bob"}` | `201` new membership; `200` if already a member |
| `GET /api/v1/rooms/{code}` | No body | `200 {"state":RoomState}` for this member |
| `POST /api/v1/rooms/{code}/leave` | `{"room_id":"r_example"}` | `200`; explicitly removes this session's membership |
| `GET /api/v1/rooms/{code}/ws?room_id=r_example` | Cookie, Origin, WebSocket upgrade and subprotocol | `101 Switching Protocols` after authorization |

Creation/join return membership metadata rather than an embedded game snapshot:

```http
POST /api/v1/rooms HTTP/1.1
Origin: https://falsepoker.example
Cookie: falsepoker_session=<opaque>
Content-Type: application/json
Idempotency-Key: 00000000000000000000000000000001

{"display_name":"Alice"}
```

```json
{
  "room_code": "a1b2c3d4e5f6",
  "room_id": "r_example",
  "player_id": "p_alice",
  "revision": "1"
}
```

The response also reports `Location: /api/v1/rooms/a1b2c3d4e5f6`. Joining during
play creates an unready spectator, never a game seat. An already joined member
gets their existing identity and current revision; join does not rename them,
change readiness, restore participation, or reset an attachment/reconnect timer.
There is no rename endpoint in v1. A new membership may choose a new display name.

Leave returns `{"room_id":"r_example","left":true}`. It requires the intended
room lifetime but no expected revision: leaving applies regardless of current
turn or game phase. It withdraws an active participant, retains their current
cards/bid as specified in the rules, transfers admin on permanent departure, and
closes every connection for that membership. A cached retry still succeeds even
if that leave deleted the room. An uncached leave by a nonmember is unavailable.

Capture successful membership metadata within the operation that creates/joins
the room, alongside its committed revision. Do not mutate membership and then
perform a separate authorized read to build the response: a concurrent leave
could otherwise turn an applied join into a reported failure. HTTP duplicate
admission stays reserved until that operation's result has been recorded.

Room information and upgrades return the same `404 ROOM_UNAVAILABLE` for an
unknown, deleted, or unauthorized room. They never reveal another room's members
or existence. An authenticated join may return `404 ROOM_UNAVAILABLE` for an
unknown code or `409 ROOM_FULL` for a full room; joining is the invitation action.
After current membership is authorized, a wrong `room_id` returns `409 STALE_ROOM`.

Session initialization has no idempotency key: it creates no room or game. A lost
initial response is retried using whatever cookie the browser received; a second
session created without receiving the first cookie has no memberships and expires.

## WebSocket handshake and messages

The browser opens the room WebSocket with the session cookie and
`Sec-WebSocket-Protocol: falsepoker.v1`. An absent/unsupported subprotocol returns
`400 UNSUPPORTED_PROTOCOL` before upgrading. Authentication, membership, room
lifetime, origin, and connection-limit failures are ordinary HTTP errors before
the upgrade. The upgrade never joins a room implicitly.

The first application message is always the recipient's complete `snapshot`.
Register the connection, cancel its membership's grace expiry, and register/queue
the initial view through one room synchronization boundary. If expiry wins that
race, reject the attachment; it cannot resurrect a removed membership. The
connection cannot submit commands until its initial snapshot is queued first.

Client mutations share this envelope; every listed field is required:

```json
{
  "type": "command",
  "request_id": "00000000000000000000000000000007",
  "room_id": "r_example",
  "expected_revision": "7",
  "game_id": "1",
  "round_id": "1",
  "action": "bid",
  "data": {
    "bid": {"category":"high_card","primary_rank":"A"}
  }
}
```

The connection selects the room and the cookie selects the actor; an additional
`player_id`, credential, or room code in this payload is invalid. Each connection
processes incoming application commands sequentially. Room locking also
serializes commands from different connections and lifecycle callbacks. Reject
commands from terminated connections; an operation already committed before
closure keeps its outcome and may be recovered through the retry cache.

| Action | Exact `data` object | Authorization and behavior |
| --- | --- | --- |
| `ready` | `{"ready":true}` or `{"ready":false}` | Any member, before play or after finish; unchanged readiness is a successful no-op |
| `start` | `{"seat_order":["p_alice","p_bob"],"shuffle_seats":false,"first_bidder":"p_alice"}` | Admin only; exact set of 2–24 ready members, each once |
| `start` (random seating/starter) | `{"seat_order":null,"shuffle_seats":true,"first_bidder":null}` | Server chooses seats/starter; explicit order plus random starter, or shuffled order plus explicit starter, are also valid |
| `bid` | `{"bid":Bid}` | Current active participant, bidding phase; opening claim or strict raise |
| `challenge` | `{}` | Current active participant, bidding phase, with a preceding bid |
| `next_round` | `{}` | Designated next starter during nonterminal resolution; admin status grants no override |

All three start fields are required. Explicit `seat_order` must be a nonempty
array and requires `shuffle_seats:false`; shuffled order requires
`seat_order:null`. `first_bidder:null` means random, otherwise it must identify a
ready participant. No client seed or card order is accepted. Start deals the
initial round immediately and creates a fresh game identity.

Every action, including ready/start, names the currently observed game and round.
Before the first game use `game_id:"0", round_id:"0"`; after finish use the
finished game's IDs until a successful replay start replaces them.

### Revision and acceptance rules

Revision is per live room, starts at 1, and continues across games. Accepted joins,
readiness changes, starts, bids, challenges, advancements, departures, and expiry
withdrawals each advance it once. A single departure's membership removal, admin
transfer, withdrawal/win, and readiness reset share one revision. Rejections,
no-op readiness, reads, connections, and subscription setup/closure do not advance
it. Connection presence is internal lifecycle state, not a v1 snapshot field.

For a new command, game/round IDs must match and `expected_revision` must equal
the current room revision. The policy is deliberately strict: even a spectator
join can invalidate a pending bid. This avoids a separate gameplay revision in
the MVP. Clients refresh/review instead of automatically applying their old intent
to a new state. A changed context gives `STALE_GAME` or `STALE_ROUND`; a changed
revision within that context gives `STALE_STATE`.

Authorization, duplicate lookup/admission, context/revision checks, game
validation, mutation, revision assignment, result-cache recording, and ordered
view enqueueing must form one serialized room operation. An HTTP/WebSocket
handler must not check a snapshot revision and then call an unguarded mutator.
For cached duplicates, lookup precedes context/revision/turn checks: replaying a
successful old command returns its original outcome instead of executing it again.

Successful commands respond only to the requesting connection:

```json
{
  "type": "ack",
  "request_id": "00000000000000000000000000000007",
  "room_id": "r_example",
  "revision": "8",
  "changed": true,
  "replayed": false
}
```

The revision is the result of that operation, not necessarily the latest revision
when the reply reaches the client. `changed:false` acknowledges a successful
no-op, which produces no additional snapshot. State changes publish a full
recipient view to every active subscription, including the originating connection.

Acknowledgments and snapshots may interleave; there is no ack-before-snapshot
promise. Acks never change displayed state. A client waits for an appropriate
snapshot to render an accepted change and never rolls back from a newer snapshot
to an older ack's revision. A successful ack means the server applied the command,
not that every other connection received it.

### Bids and cards

Card objects are `{"rank":"9","suit":"clubs"}`. Ranks are `"9"`, `"10"`, `"J"`,
`"Q"`, `"K"`, `"A"`; suits are `clubs`, `diamonds`, `hearts`, `spades`.

| Bid `category`, weakest to strongest | Required fields besides `category` |
| --- | --- |
| `high_card`, `pair` | `primary_rank` |
| `two_pairs` | `primary_rank` (higher pair), `secondary_rank` (lower pair) |
| `low_straight`, `high_straight` | None |
| `three_of_a_kind` | `primary_rank` |
| `full_house` | `primary_rank` (triple), `secondary_rank` (pair) |
| `flush` | `suit` |
| `four_of_a_kind` | `primary_rank` |
| `straight_flush`, `royal_straight_flush` | `suit` |

Only those fields are permitted; unused fields must be omitted, not `null` or
zero. The two ranks in a full house or two pairs must differ. Suits never order
bids. An ordinary flush needs any five of its suit; straight flush is 9–K and
royal straight flush is 10–A. A legal bid may be a bluff; submitting it does not
evaluate whether it exists. Challenges evaluate the specific preceding claim
against all currently dealt cards, including withdrawn players' cards.

## Snapshot schema and visibility

WebSocket state delivery is `{"type":"snapshot","state":RoomState}`. HTTP GET
uses the same state schema without the WebSocket `type`. There are no deltas;
each snapshot completely replaces the previous view for that room lifetime.

| `RoomState` field | Type and meaning |
| --- | --- |
| `room_id`, `room_code` | Room lifetime identity and invitation code |
| `revision`, `game_id` | Decimal strings; game ID `"0"` while `game:null` |
| `recipient_id` | Authenticated public player ID |
| `admin_id` | Current sole admin, always a current member |
| `role` | `participant` if a non-eliminated, non-withdrawn member of this game; otherwise `spectator` |
| `members` | Join-order array of `{player_id, display_name, ready}`; current members only |
| `game` | `null` before the first game, otherwise `GameState` below |

| `GameState` field | Type and meaning |
| --- | --- |
| `phase` | `playing` or `finished` |
| `round_phase` | `bidding` or `resolution` |
| `round_id` | Decimal string, scoped to the current game |
| `elimination_limit` | Integer, current round's exclusive threshold |
| `starter_id` | Original starter of this round, possibly since departed |
| `next_starter_id` | Designated survivor during nonterminal resolution; otherwise `null` |
| `current_player_id` | Current bidder/challenger during bidding; otherwise `null` |
| `players` | Fixed seat-order array of `PlayerState`, including former participants |
| `last_bid` | `null` or `{player_id, bid:Bid}` |
| `last_result` | `null` or `RoundResult`; retained after a new deal |
| `winner_id` | Sole survivor when finished; otherwise `null` |

`PlayerState` is `{player_id, display_name, required_hand_size, hand_count,
eliminated, withdrawn, hand}`. Counts are integers and flags are booleans.
`display_name` is retained from game start even if the member leaves; it labels
past bids/results for a freshly connected client. The roster is replaced on a
new game. `required_hand_size` is the engine's retained size; for inactive players
it is historical, not a promise to deal again. `hand_count` is the actual dealt
count, which can differ from required size after a penalty.

`hand` is a card array when authorized, `null` when hidden, and `[]` when that
player has no current cards. Arrays are never encoded as `null` except hidden
hands and the explicitly optional fields documented above.

`RoundResult` has `round_id`, `elimination_limit`, `challenged_bid:{player_id,bid}`,
`challenger_id`, `claim_satisfied` (boolean), `loser_id`, and
`revealed_hands:[{player_id,cards:[Card]}]`. It records the completed round's limit
and all its dealt cards, including departed or newly eliminated participants.

Visibility is selected on the server for every snapshot:

- During bidding, participants receive their own current hand only. Opponents'
  nonempty hands are `null`. Admin privileges do not change this.
- All spectators, including late joiners and eliminated players, receive all
  survivors' current hands. Withdrawn players' retained bidding cards are hidden
  from spectators. The UI hides spectator hands by default and reveals them only
  while the peek control is held; there is no network `peek` command.
- During resolution or finish, every currently dealt hand is public, including
  withdrawn/eliminated players' cards. A departure win reveals current hands even
  without a challenge; it does not invent a round loser or result.
- `last_result` is public to all room members and may refer to an older round.
  Its revealed cards must not be merged into the current dealt set. A departure
  win may leave it `null` or retain the previous completed round's result.
- On replay, roles are recalculated, all hands are freshly dealt, and the previous
  game's result/winner is cleared. A previous spectator who participates receives
  only their own new hand. A surviving winner remains `participant` in the
  finished game's view until the next game determines a new role.

No snapshot contains undealt cards, RNG seeds, session credentials, or an
authoritative engine pointer. A new client can render the current phase, seats,
counts, turn, bid, latest reveal, and winner from one snapshot.

## Request retries and duplicate handling

The guarantee is **at most one application within a bounded retry window** in a
live process, not permanent exactly-once delivery. Retain admitted outcomes for
5 minutes from first processing, without extending that expiry on retries.
Clients stop automatic retries 4 minutes after their first send; after that they
refresh state and report an uncertain outcome instead of blindly resending.
The WS cache is lost with its room, and all caches are lost with their session
or server restart. The HTTP leave cache survives room deletion for its remaining
retention window. An evicted request ID must never be reused as a new intent.
After retention, the server may be unable to distinguish an old ID from a new one.

HTTP creation/join/leave require `Idempotency-Key` in the request-ID format.
Keys are scoped to the authenticated session across those HTTP routes. Store the
normalized method/path/body fingerprint and original HTTP status/body. Equal
retries replay that response with `Idempotency-Replayed: true`; they do not create
another room, reset readiness, or repeat withdrawal. The session-level HTTP cache
retains a successful leave response after membership/room removal. Current session
and origin checks still apply before cache access; cached membership metadata is
not authorization for subsequent room operations.

WebSocket request IDs are scoped to `(room_id, authenticated player_id)` across
all of that player's connections and all games in that live room. Cache the
validated command fingerprint and terminal ack/error, including its original
revision. Replaying an identical command returns the original outcome with
`replayed:true`, even when the current game, round, turn, or admin has changed.
It emits no extra snapshot and applies no penalty again. Current session and
membership authorization still precede this lookup. A permanently departed
player cannot reconnect just to read the old command cache.

The same key with different normalized content gives `REQUEST_ID_REUSED` and does
not replace the original record. JSON object key order and whitespace do not
change a fingerprint; expected revision, IDs, action, and validated payload do.
Simultaneous duplicates wait for the same serialized decision and receive the
same outcome. Rejected, admitted commands such as stale/out-of-turn actions are
cached too; retrying them later does not turn them into accepted actions.

Reserve retry-cache capacity before any mutation. Never evict an unexpired record
to admit another request. A full cache returns `RETRY_WINDOW_FULL` without mutation
and permits retry after capacity becomes available. Authentication, origin,
malformed-input, and admission/rate-limit failures are not cached. Cache full
results must not overwrite an earlier outcome for the same key. HTTP cache
serialization must not introduce a lock order opposite to room locking.

If a command was applied but its ack was lost, reconnect with the same cookie,
receive the initial snapshot, then resend the exact original ID/envelope within
the retry window. The original expected revision is intentional: duplicate lookup
comes first. The replayed ack can have an older revision than the reconnect
snapshot; it never rolls state backward. A new action after a rejection uses a
new ID and freshly reviewed context. Do not automatically reissue a stale
challenge with a new expected revision.

## Errors, limits, and connection closure

HTTP failures use this envelope; `request_id` is `null` if none can be validated:

```json
{
  "request_id": "00000000000000000000000000000009",
  "error": {
    "code": "INVALID_REQUEST",
    "message": "A required field is missing.",
    "retryable": false
  }
}
```

WebSocket application failures add `type:"error"`, `room_id`, `revision`, and
`replayed`. Revision/context details are available only on an authenticated member
connection. `message` is human-readable; clients branch on `code`. Errors never
include cards, credentials, internal paths, or stack traces.

| Code | HTTP status where applicable | Meaning / response |
| --- | --- | --- |
| `INVALID_REQUEST`, `INVALID_MEMBER`, `INVALID_BID`, `INVALID_START`, `UNKNOWN_COMMAND` | `400` | Invalid shape, name, bid parameters, start choices, or action |
| `UNSUPPORTED_PROTOCOL` | `400` | Unsupported/missing WebSocket subprotocol |
| `AUTH_REQUIRED`, `SESSION_EXPIRED` | `401` | Initialize or explicitly replace the session |
| `ORIGIN_NOT_ALLOWED` | `403` | Origin is not the configured application origin |
| `NOT_ADMIN`, `NOT_PARTICIPANT`, `INACTIVE_PLAYER` | `403` | Member lacks admin/game action rights |
| `ROOM_UNAVAILABLE` | `404` | No accessible live membership/room for this operation |
| `NOT_FOUND`, `METHOD_NOT_ALLOWED` | `404`, `405` | Unknown HTTP route or unsupported method |
| `STALE_ROOM`, `STALE_GAME`, `STALE_ROUND`, `STALE_STATE` | `409` | Refresh/review; never reinterpret the old action against a newer state |
| `NO_GAME`, `WRONG_PHASE`, `GAME_IN_PROGRESS` | `409` | Action is unavailable in the current game/phase |
| `OUT_OF_TURN`, `NO_BID`, `BID_NOT_HIGHER` | `409` | Gameplay validation failed |
| `REQUEST_ID_REUSED`, `ROOM_FULL`, `MEMBERSHIP_LIMIT` | `409` | Key conflicts with original content, or member/room membership capacity reached |
| `RETRY_WINDOW_FULL`, `RATE_LIMITED`, `CONNECTION_LIMIT` | `429` | Admission rejected before mutation; retry after the supplied delay |
| `PAYLOAD_TOO_LARGE` | `413` | Body/message exceeds the byte limit |
| `UNSUPPORTED_MEDIA_TYPE` | `415` | HTTP mutation is not JSON |
| `INTERNAL` | `500` | Unexpected server failure; do not expose implementation details |
| `SERVER_BUSY`, `SHUTTING_DOWN` | `503` | Process capacity or shutdown rejects new work |

Unknown HTTP routes return `404`; unsupported methods return `405` with `Allow`.
HTTP `429`/`503` responses include `Retry-After`; equivalent WebSocket admission
errors include `retry_after_ms`. Only non-admitted transient failures have
`retryable:true`; a cached rejection requires a newly reviewed request, not a
retry expected to become valid later. An uncertain post-mutation delivery failure
closes the connection; it must not falsely report that the action was unapplied.

These are initial operational defaults, not gameplay changes. Limits are
configurable, with room-member capacity at least 24 and at least two simultaneous
connections permitted per membership. Process admission limits do not change
the 2–24 game-participant bounds.

| Limit | Default |
| --- | --- |
| HTTP JSON body / complete incoming WebSocket message | 8 KiB each, including fragmented message aggregate |
| Outgoing JSON message | 64 KiB; overflow is a server error, never truncated state |
| Current room members, including spectators and grace-period members | 64 |
| Room memberships per anonymous session | 8 |
| Simultaneous connections per membership | 4; excess attachment rejected without closing existing tabs |
| Live rooms / anonymous sessions / total WebSockets per process | 128 / 1,024 / 512 |
| HTTP retry records per session / WS retry records per player-room | 128 / 256, each retained 5 minutes |
| Combined WS retry records per live room, including departed identities | Member capacity × 256; same retention/admission policy |
| Pending room views per connection | 16, including the initial view |
| Pending transport output envelopes per connection | At most 16, including acks/errors; no unbounded intermediate queue |
| Mutations per session | 10 per second, burst 20; cached retries count for ingress rate limits |
| New anonymous session requests per source address | 30 per minute, burst 30; trust forwarded addresses only from a configured proxy |

Capacity checks happen atomically with admission. The server rejects new rooms or
sessions with `SERVER_BUSY` instead of evicting occupied rooms. Expired anonymous
sessions and retry records are cleaned up. Occupied rooms do not expire merely
to make capacity available. Body/read limits and process limits apply before
allocating large queues; cached retries do not need another retry-cache slot.

All writes for one connection, including acks/errors and heartbeat control, are
serialized by its transport writer outside room locks. A full room-view or
transport-output queue closes only that connection, drops its pending output,
and requires fresh snapshot synchronization. Do not block a room waiting for a
socket write or ack queue slot.

The server sends WebSocket Ping at least every 20 seconds and requires a matching
Pong within 45 seconds of its most recent Pong/initial attachment. Each socket
write has a 10-second deadline. Closing after a missed heartbeat starts grace
when the server detects the last connection loss, not at an unknowable physical
network-disconnection instant. There is no gameplay turn deadline.

| WebSocket close code | Meaning / client behavior |
| --- | --- |
| `1000` | Normal connection closure; last-tab closure starts grace, not explicit leave |
| `1001` | Server shutdown; active state will not survive process restart |
| `1002`, `1003`, `1007` | Invalid framing, unsupported binary message, or invalid UTF-8 |
| `1008` | Repeated malformed input or rate abuse; correct the client before retrying |
| `1009` | Incoming message too large |
| `1011` | Unexpected server failure |
| `4001` | Session no longer valid; stop commands and replace identity explicitly |
| `4003` | Membership ended through explicit leave or grace expiry; stop automatic reconnect |
| `4004` | Room no longer available; return to join/create flow |
| `4008` | Slow consumer; reconnect and synchronize if membership still exists |

Application errors normally keep the connection open. After three consecutive
malformed application messages, close with `1008`; a valid message resets that
counter. Three consecutive rate-limited application messages also close that
connection with `1008`; a message admitted by the ingress limiter resets that
separate counter. Oversized/binary/invalid-UTF-8 messages close immediately as above.
Close frames/reasons are best effort: clients also handle abrupt connection loss
and must not depend on a final application message. Reconnect uses exponential
backoff with jitter, starting around 500 ms and capped at 5 seconds.

## Reconnect, leave, replay, and shutdown

Membership, game participation, and connection identity are distinct. Opening
another authenticated tab adds a subscription, not a seat, new hand, or admin.
Every tab receives its player's authorized view; only the requesting tab receives
the command outcome. Conflicting tab actions are serialized, and all tabs share
the player's duplicate-request scope.

After the last connection closes, the member has 60 seconds to attach again.
During grace, the member, seat, readiness, hand, and admin rights remain. Play
continues until their turn and then waits; it never skips them merely for being
disconnected. Successful authenticated attachment cancels expiry. Timer callbacks
check membership/timer generation and current connection count under the room
lock, so an obsolete timer cannot remove a reconnected or newly joined member.
An attachment at or beyond the stored deadline processes expiry and is rejected,
even if the timer callback has not run yet. An attachment linearized before the
deadline cancels that expiry; callbacks never withdraw members early. Use a
monotonic server clock for durations; client clocks do not decide membership expiry.

The HTTP-to-WebSocket edge uses the same grace budget: a newly created/joined
membership with no connection gets 60 seconds to attach its first WebSocket.
If it never attaches, normal permanent-departure handling runs. Repeated HTTP
join/GET calls do not prolong this timer. This makes abandoned HTTP joins obey
the same cleanup policy without adding idle-lobby eviction or a turn timer.

After grace expires, remove the membership through the room's serialized leave
path. Withdraw current participants, retain current cards/bid through challenge,
transfer designated starter/admin as required, and end immediately if one
participant remains. Spectators and grace-period members count toward occupied
rooms. Delete a room immediately when its final member is permanently removed.

Reconnect uses the existing cookie and room lifetime to upgrade again; it does
not call join. The first message is a fresh full snapshot, followed by later
revisions. Historical deltas are not replayed and there is no `resume_from`
cursor. Clients discard old-connection output and accept the new connection's
initial snapshot as the baseline. On one connection, snapshots advance strictly
by revision; gaps are safe because each snapshot is complete. HTTP snapshots may
arrive ahead of queued WS snapshots; ignore revisions older than the currently
rendered state. An old `room_id` is never merged with a new room's state.

If grace already expired, GET/upgrade returns `404 ROOM_UNAVAILABLE`. Explicitly
joining again is a new membership: during play it is a spectator and cannot
restore withdrawn participation. An eliminated member who never left stays a
spectator without needing to rejoin. A finished game clears all readiness but
retains connections and the final view. Members may ready again, then the current
admin starts a new game through the same command with fresh observed context.

Shutdown stops admitting mutations/upgrades, closes sockets with `1001` when
possible, cancels timers, and releases subscriptions. It does not persist state.
After restart, old credentials fail and old rooms are unavailable; the client
explains the loss and offers a new session/room rather than claiming recovery.

## Worked successful exchange

Alice, Bob, and Cara have separate anonymous sessions. Alice creates the room,
Bob/Cara join using its code, and each opens its own authenticated WebSocket.
Each HTTP mutation below uses a distinct idempotency key. Connection setup does
not advance room revisions.

| Step | Request / outcome | Room revision |
| --- | --- | --- |
| Create | Alice: `POST /api/v1/rooms`, `{"display_name":"Alice"}` → `201`, admin Alice | `1` |
| Join | Bob: `POST .../join`, `{"display_name":"Bob"}` → `201` | `2` |
| Join | Cara: `POST .../join`, `{"display_name":"Cara"}` → `201` | `3` |
| Synchronize | All attach to `.../ws?room_id=r_example`; first message is each recipient's snapshot | `3` |
| Ready | Alice/Bob/Cara each send `ready:true` with observed revisions `3`, `4`, `5`, game/round `0` | `4`, `5`, `6` |
| Start | Alice sends explicit seats `[p_alice,p_bob,p_cara]`, first bidder Alice, expected revision `6`, game/round `0` | `7` |
| Opening | Alice bids high card A, expected revision `7`, game/round `1` | `8`; Bob's turn |
| Raise | Bob bids pair K, expected revision `8`, game/round `1` | `9`; Cara's turn |
| Challenge | Cara challenges, expected revision `9`, game/round `1` | `10`; Bob loses, required hand size becomes 2 |
| Advance | Bob sends `next_round`, expected revision `10`, game/round `1` | `11`; round `2`, Bob starts |

For this illustrative deal, Alice has 9 clubs, Bob A hearts, and Cara K spades.
The high-card bid is true, the pair bid is a legal higher bluff, and the pair is
false. The server chose the cards; clients never supply the deal.

Alice's first-round bidding snapshot at revision 7 is complete:

```json
{
  "type": "snapshot",
  "state": {
    "room_id": "r_example",
    "room_code": "a1b2c3d4e5f6",
    "revision": "7",
    "game_id": "1",
    "recipient_id": "p_alice",
    "admin_id": "p_alice",
    "role": "participant",
    "members": [
      {"player_id":"p_alice","display_name":"Alice","ready":true},
      {"player_id":"p_bob","display_name":"Bob","ready":true},
      {"player_id":"p_cara","display_name":"Cara","ready":true}
    ],
    "game": {
      "phase": "playing",
      "round_phase": "bidding",
      "round_id": "1",
      "elimination_limit": 6,
      "starter_id": "p_alice",
      "next_starter_id": null,
      "current_player_id": "p_alice",
      "players": [
        {"player_id":"p_alice","display_name":"Alice","required_hand_size":1,"hand_count":1,"eliminated":false,"withdrawn":false,"hand":[{"rank":"9","suit":"clubs"}]},
        {"player_id":"p_bob","display_name":"Bob","required_hand_size":1,"hand_count":1,"eliminated":false,"withdrawn":false,"hand":null},
        {"player_id":"p_cara","display_name":"Cara","required_hand_size":1,"hand_count":1,"eliminated":false,"withdrawn":false,"hand":null}
      ],
      "last_bid": null,
      "last_result": null,
      "winner_id": null
    }
  }
}
```

Bob's view hides Alice/Cara and includes Bob's own card. A fourth, nonparticipating
member would have role `spectator` and receive all three survivors' hands.

Bob's raise and Cara's challenge are sent on their respective connections:

```json
{
  "type": "command",
  "request_id": "00000000000000000000000000000008",
  "room_id": "r_example",
  "expected_revision": "8",
  "game_id": "1",
  "round_id": "1",
  "action": "bid",
  "data": {"bid":{"category":"pair","primary_rank":"K"}}
}
```

```json
{
  "type": "command",
  "request_id": "00000000000000000000000000000009",
  "room_id": "r_example",
  "expected_revision": "9",
  "game_id": "1",
  "round_id": "1",
  "action": "challenge",
  "data": {}
}
```

Cara receives ack revision `10`. Every member receives the following public
`last_result` inside their complete revision-10 snapshot; all three current
`players[].hand` arrays are revealed, Bob has `required_hand_size:2` but
`hand_count:1`, `current_player_id:null`, and `next_starter_id:"p_bob"`:

```json
{
  "round_id": "1",
  "elimination_limit": 6,
  "challenged_bid": {"player_id":"p_bob","bid":{"category":"pair","primary_rank":"K"}},
  "challenger_id": "p_cara",
  "claim_satisfied": false,
  "loser_id": "p_bob",
  "revealed_hands": [
    {"player_id":"p_alice","cards":[{"rank":"9","suit":"clubs"}]},
    {"player_id":"p_bob","cards":[{"rank":"A","suit":"hearts"}]},
    {"player_id":"p_cara","cards":[{"rank":"K","suit":"spades"}]}
  ]
}
```

Bob advances explicitly:

```json
{
  "type": "command",
  "request_id": "0000000000000000000000000000000a",
  "room_id": "r_example",
  "expected_revision": "10",
  "game_id": "1",
  "round_id": "1",
  "action": "next_round",
  "data": {}
}
```

Revision 11 is a fresh full snapshot: round ID `2`, starter/current player Bob,
`next_starter_id:null`, `last_bid:null`, hand counts `[1,2,1]`, and the unchanged
round-1 result. Alice sees only her new current card and the old public result;
Bob's two new cards and Cara's new card are hidden from her.

To finish this exchange, Alice explicitly leaves over HTTP (revision 12), then
Cara leaves (revision 13). Bob wins immediately, with no challenge or new deal.
Bob's complete final snapshot has `phase:"finished"`, `round_phase:"resolution"`,
`winner_id:"p_bob"`, no current/next player, both departed players marked withdrawn,
all round-2 hands revealed, and Bob unready. Alice/Cara still have names in the
game roster. The retained `last_result` is still round 1. Bob is the remaining
admin regardless of which survivor received admin rights after Alice's departure.

Alice may join again (revision 14). Bob and Alice ready (revisions 15 and 16),
then Bob starts with those two ready members (revision 17). The new snapshot has
`game_id:"2"`, round ID `1`, one card each, and no old result or winner. Roles and
current-card privacy are recomputed. An elimination instead of departure uses
the same snapshot fields: the eliminated member remains in `members`, becomes a
spectator, and receives no new hand in subsequent rounds.

## Failure and recovery examples

At actual revision 9, Bob retries a *new* bid with old expected revision 8 and
a new request ID. The server rejects it without advancing revision:

```json
{
  "type": "error",
  "request_id": "00000000000000000000000000000020",
  "room_id": "r_example",
  "revision": "9",
  "replayed": false,
  "error": {"code":"STALE_STATE","message":"Room state changed; review a current snapshot.","retryable":false}
}
```

If Bob instead retries the exact already accepted raise ID `...08`, the response
is its original revision-9 ack with `replayed:true`, not `STALE_STATE`, even after
Cara's challenge. No second bid or snapshot is emitted.

At revision 7, Bob attempts a challenge with correct game/round/revision but it
is Alice's turn. This distinguishes stale context from current-state validation:

```json
{
  "type": "error",
  "request_id": "00000000000000000000000000000021",
  "room_id": "r_example",
  "revision": "7",
  "replayed": false,
  "error": {"code":"OUT_OF_TURN","message":"It is another player's turn.","retryable":false}
}
```

| Scenario | Required outcome |
| --- | --- |
| Alice challenges before the opening bid, with current context | `NO_BID`; no mutation or snapshot |
| Equal/lower/suit-only raise | `BID_NOT_HIGHER`; no mutation |
| Nonadmin tries start / admin spectator tries bid | `NOT_ADMIN` / `NOT_PARTICIPANT` |
| Repeat challenge in the same resolved round with a new ID and old revision | `STALE_STATE`; it cannot resolve twice |
| New command in resolved/finished phase with current revision | Appropriate `WRONG_PHASE` / `INACTIVE_PLAYER`; no mutation |
| Old-round command after redeal / old-game command after replay | `STALE_ROUND` / `STALE_GAME`, even if it is that actor's turn again |
| Same request ID with different action/payload | `REQUEST_ID_REUSED`; original cached outcome unchanged |
| Ack lost after a successful challenge | Reconnect snapshot contains result; identical retry returns original ack, no second penalty |
| One of two tabs closes | Other tab continues; no disconnect grace or admin transfer |
| Last tab closes, reconnect wins before expiry | Same member/seat/hand; fresh initial snapshot followed by later revisions |
| Expiry wins before reconnect | Permanent departure; stale timer/upgrade cannot resurrect membership |
| Departed bidder's claim is challenged | Current dealt union includes their cards; false claim records them as loser without another penalty |
| Last member permanently leaves | Room deleted, all subscriptions/timers released; new GET/upgrade unavailable |
| Subscriber queue fills | Only its socket closes with `4008`; room continues, reconnect gets a fresh baseline |
| Unauthenticated or unauthorized GET/upgrade | `401` or generic `404`, no hands or member list |
| Server restarts | Session/room state lost; explicit new-session/create/join flow |

## Implementation mapping and verification

The wire types are explicit transport DTOs, not direct JSON marshaling of engine
state or Go enum values. Existing room views supply the privacy projection and
revision; existing subscriptions supply ordered detached snapshots. The following
table assigns integration work to its phase; HTTP/session work is implemented,
while WebSocket/lifecycle work remains. Rules stay outside transport:

| Contract | Existing support / required implementation |
| --- | --- |
| Create/join/info/leave | Registry and room APIs exist; 3.2 adds sessions, HTTP DTOs, origin/input/capacity checks, HTTP retry cache, and room lifetime IDs |
| Retained participant display names | 3.2 captures a small game roster for views/results; engine remains identity-based |
| Ready/start/bid/challenge/next round | Room mutations exist; 3.3 adds atomic expected-context/revision and WS duplicate handling **inside room coordination**, plus structured error mapping |
| Multiple tabs / initial snapshot / overflow | Room subscriptions exist; 3.3 wires bounded writer/heartbeat/close behavior, 3.4 serializes connection counts and timers with attach/leave |
| 60-second initial attach and reconnect grace | 3.4 implements controllable timers, generation checks, authoritative deadline guards, and shutdown cleanup |
| Full privacy projection / replay | Room views already filter hands; transport normalizes names, nullable fields, arrays, card/rank/suit enums, and decimal-string counters |
| Production shuffle unpredictability | Before public use, replace predictable production RNG while retaining deterministic test injection; no seed endpoint is exposed |
| Complete network evidence | 3.5 exercises real HTTP/WS payloads, lost acks, stale context, disconnects, and independent rooms |

Existing errors map without exposing raw Go error strings: room not-found/closed/
not-member becomes `ROOM_UNAVAILABLE`; an already-member join becomes HTTP 200;
room invalid-member/start/not-admin/game-in-progress/no-game errors map to their
named wire codes. `ErrStaleContext` is refined into `STALE_GAME` or `STALE_ROUND`.
Domain unknown/inactive-player errors become `NOT_PARTICIPANT`/`INACTIVE_PLAYER`;
wrong-phase/out-of-turn/no-bid/bid-not-higher/invalid-bid map to their named codes.
Internal card/deck/RNG failures are `INTERNAL`, and invitation-code allocation
exhaustion is `SERVER_BUSY`. Subscription slow-consumer/member-departure/room-close
reasons map to close codes `4008`/`4003`/`4004`, respectively.

Features reach full conformance as their dependent phases finish. The 3.2 server
can exercise HTTP before WS/lifecycle support is complete; it must be documented
as that increment rather than advertised as the complete multiplayer protocol.

For protocol documentation changes, verify fenced JSON examples parse, manually check
the worked revisions/turns/claim outcome, map every action/error above to rules
and room APIs, and run `git diff --check`. Networking tests belong to 3.2–3.5;
this document specifies behavior rather than implementing it.

References: [WebSocket framing, origin model, and close codes (RFC 6455)](https://www.rfc-editor.org/rfc/rfc6455.html),
[HTTP semantics (RFC 9110)](https://www.rfc-editor.org/rfc/rfc9110.html), and
[cookie attributes (MDN)](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Set-Cookie).
