# False Poker

A real-time multiplayer bluffing card game.

## Run locally

Install Go 1.27.1 or later. From the repository root:

```bash
go run ./cmd/server -dev-http
```

This listens on `127.0.0.1:8080` and accepts the application origin
`http://localhost:8080`. `-dev-http` explicitly permits non-Secure cookies on
loopback HTTP. Without it cookies are Secure; use that mode with an HTTPS public
origin and a reverse proxy. The server itself serves HTTP, not TLS.

To use another port, change both the listener and the public origin:

```bash
go run ./cmd/server -dev-http -addr 127.0.0.1:8090 -origin http://localhost:8090
```

Flags `-max-rooms` and `-max-members` configure room capacity (defaults 128 and
64). Member capacity must permit at least 24 players. Transport limits are
configured through `transport.Config` and default to the [protocol](docs/protocol.md).
Ctrl+C stops admission, closes WebSockets, and shuts the HTTP server down. All active state is lost
when the process stops.

## Try the HTTP flow

Keep the server running and use a second terminal. Cookie jars keep the two
anonymous players separate; credentials are not returned in JSON.

```bash
FP_TMP_DIR=$(mktemp -d)

curl --fail --silent --show-error http://localhost:8080/health

curl --fail --silent --show-error -c "$FP_TMP_DIR/alice.cookies" \
  -H 'Origin: http://localhost:8080' -H 'Content-Type: application/json' \
  --data '{}' http://localhost:8080/api/v1/session

curl --fail --silent --show-error -b "$FP_TMP_DIR/alice.cookies" \
  -H 'Origin: http://localhost:8080' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: 00000000000000000000000000000001' \
  --data '{"display_name":"Alice"}' http://localhost:8080/api/v1/rooms
```

Copy `room_code` and `room_id` from that response into these variables:

```bash
ROOM_CODE='paste_room_code_here'
ROOM_ID='paste_room_id_here'

curl --fail --silent --show-error -c "$FP_TMP_DIR/bob.cookies" \
  -H 'Origin: http://localhost:8080' -H 'Content-Type: application/json' \
  --data '{}' http://localhost:8080/api/v1/session

curl --fail --silent --show-error -b "$FP_TMP_DIR/bob.cookies" \
  -H 'Origin: http://localhost:8080' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: 00000000000000000000000000000002' \
  --data '{"display_name":"Bob"}' \
  "http://localhost:8080/api/v1/rooms/$ROOM_CODE/join"

curl --fail --silent --show-error -b "$FP_TMP_DIR/bob.cookies" \
  "http://localhost:8080/api/v1/rooms/$ROOM_CODE"
```

The snapshot shows Alice as admin, both players unready, string counters, and
`game:null`. An unauthenticated read returns 401; another authenticated session
that has not joined gets the same 404 as an unknown room. Repeating the exact
create/join request with its original key returns the cached result for five
minutes. A new action needs a new random 32-hex-character key; the fixed keys
above are illustrative for these fresh sessions.

Leave explicitly as each player to release the room:

```bash
curl --fail --silent --show-error -b "$FP_TMP_DIR/alice.cookies" \
  -H 'Origin: http://localhost:8080' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: 00000000000000000000000000000003' \
  --data "{\"room_id\":\"$ROOM_ID\"}" \
  "http://localhost:8080/api/v1/rooms/$ROOM_CODE/leave"

curl --fail --silent --show-error -b "$FP_TMP_DIR/bob.cookies" \
  -H 'Origin: http://localhost:8080' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: 00000000000000000000000000000004' \
  --data "{\"room_id\":\"$ROOM_ID\"}" \
  "http://localhost:8080/api/v1/rooms/$ROOM_CODE/leave"
```

The final leave deletes the room. Keep cookie jars private and delete this
temporary directory when finished.

## Try a live WebSocket

With the server running, open `http://localhost:8080/health` in your browser,
then open its developer console. Run this snippet on that page so the browser
sends the configured origin and manages the HttpOnly session cookie:

```javascript
var requestID = () => crypto.randomUUID().replaceAll('-', '');
var post = (path, data, key) => fetch('/api/v1/' + path, {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    ...(key ? {'Idempotency-Key': key} : {}),
  },
  body: JSON.stringify(data),
}).then(async response => {
  const result = await response.json();
  if (!response.ok) throw result;
  return result;
});
await post('session', {});
var membership = await post('rooms', {display_name: 'Alice'}, requestID());
var state;
var socket = new WebSocket(
  `ws://${location.host}/api/v1/rooms/${membership.room_code}/ws?room_id=${membership.room_id}`,
  'falsepoker.v1',
);
socket.onmessage = event => {
  const message = JSON.parse(event.data);
  if (message.type === 'snapshot') state = message.state;
  console.log(message);
};
socket.onclose = event => console.log('closed', event.code, event.reason);
```

After the initial snapshot appears, send readiness:

```javascript
socket.send(JSON.stringify({
  type: 'command', request_id: requestID(), room_id: state.room_id,
  expected_revision: state.revision, game_id: state.game_id,
  round_id: state.game?.round_id ?? '0',
  action: 'ready', data: {ready: true},
}));
```

You should see an acknowledgment and a new snapshot with `ready:true`. Their
arrival order is unspecified. Each new action needs a new request ID and the
latest snapshot context. See [the protocol](docs/protocol.md) for start, bid,
challenge, and next-round payloads. Another tab shares this player; use a separate
browser profile for a second player, initialize its session, and join the room.
Closing the socket starts grace only after this player's final tab closes.
Reopen the same URL with the same browser cookie within 60 seconds to receive a
fresh snapshot of the same membership and hand; no HTTP rejoin is needed.
Complete automated multiplayer scenarios can also be run directly:

```bash
go test -v ./internal/integration/... -count=1
go test -race ./internal/integration/...
```

These headless clients start a server on an ephemeral loopback port and use real
HTTP/WebSocket messages. The verbose run prints round outcomes through complete
games and replay. Fixtures use seed 42 for reproducible seats, deals and shuffles,
plus a controlled clock for reconnect/expiry; normal server randomness is unchanged.
Every received payload is checked for authorized card visibility and unexpected
private fields. Scenarios also cover lost acknowledgments, retries, conflicting
tabs, spectator rejoin, and independent rooms. No running server, browser,
database, or external service is needed.

## Verification and current limits

```bash
gofmt -l cmd internal
go mod tidy -diff
go test -race -count=1 -cover ./...
go vet ./...
git diff --check
```

Tests use ephemeral local HTTP/TLS/WebSocket servers and require no database or cloud
service. Race detection needs a supported platform and a C compiler.

HTTP and WebSocket gameplay work in memory; there is no UI or persistence yet.
Reconnect with the same cookie and room lifetime receives a fresh snapshot and
can recover cached command outcomes for five minutes. Initial attachment and
last-connection loss both allow 60 seconds. Expiry applies normal departure
rules, transfers admin when needed, and deletes an empty room. Connected players
have no turn timeout; occupied lobbies and finished rooms have no inactivity
timeout. Restart loses credentials and rooms; it cannot restore a previous game.

- [Protocol](docs/protocol.md)
- [Architecture](docs/architecture.md)
- [Game rules](docs/game-rules.md)
- [Requirements](docs/requirements.md)
- [Implementation plan](docs/implementation-plan.md)
