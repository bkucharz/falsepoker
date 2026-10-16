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
Ctrl+C stops admission and shuts the HTTP server down. All active state is lost
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

## Verification and current limits

```bash
gofmt -l cmd internal
go mod tidy -diff
go test -race -count=1 -cover ./...
go vet ./...
git diff --check
```

Tests use ephemeral local HTTP/TLS servers and require no database or cloud
service. Race detection needs a supported platform and a C compiler.


- [Protocol](docs/protocol.md)
- [Architecture](docs/architecture.md)
- [Game rules](docs/game-rules.md)
- [Requirements](docs/requirements.md)
- [Implementation plan](docs/implementation-plan.md)
