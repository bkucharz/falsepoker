# False Poker

A real-time multiplayer bluffing card game built as a modular monolith. The Go
server owns game and room state; the React client provides the responsive game
table. Active rooms are stored in memory.

## Features

- Private rooms with invitation links and anonymous player identities
- Server-authoritative bidding, challenges, dealing, and elimination
- Real-time updates over WebSockets
- Reconnection during a 60-second grace period
- Responsive gameplay for desktop and mobile browsers
- Spectator access with hold-to-peek private hands

## Run locally

Requirements: Go 1.27.1, Node.js 22.23.1, and npm 10.9.8. Install frontend
dependencies from the lockfile:

```sh
npm --prefix web ci
```

Start the backend and frontend in separate terminals from the repository root.

```sh
go run ./cmd/server -dev-http -origin http://localhost:5173
```

```sh
npm --prefix web run dev
```

Open <http://localhost:5173>. Use another browser profile or a private window
for another player. To test on a phone, connect it and the computer to the same
network; see the [browser setup guide](web/README.md).

## Checks

Run the backend and frontend checks from the repository root:

```sh
gofmt -l cmd internal
go mod tidy -diff
go vet ./...
go test -race -count=1 -cover ./...
npm --prefix web run format:check
npm --prefix web run typecheck
npm --prefix web test
npm --prefix web run build
npm --prefix web run test:e2e
git diff --check
```

The Playwright suite starts local Go and Vite servers. Install Chromium first
using the [browser testing guide](web/README.md). CI runs these checks on pull
requests and pushes to `main`; see [CI documentation](docs/ci.md).

## Project documentation

- [Game rules](docs/game-rules.md)
- [Requirements](docs/requirements.md)
- [Architecture](docs/architecture.md)
- [Network protocol](docs/protocol.md)
- [Implementation plan](docs/implementation-plan.md)
- [Continuous integration](docs/ci.md)

## Current limits

Room, session, and game state exist only in memory. Restarting the server clears
them. The MVP has no accounts, database, matchmaking, chat, or production
deployment configuration. HTTPS/WSS and hosting are deferred.
