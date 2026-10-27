# False Poker browser client

Phase 4.1 provides the responsive lobby and room connection lifecycle. Phase 4.2
adds private cards, guided bids, challenges, round results, and spectator peek
controls. Phase 4.3 expands full-game recovery and multiplayer browser scenarios.

## Run locally

Use Go 1.27.1 (see `../go.mod`), Node 22.23.1 (`.node-version`), and npm 10.9.8.
Use a current browser with Web Locks, WebSocket, and BigInt support. Web Locks
requires localhost or HTTPS. Install dependencies from the repository root:

```sh
npm --prefix web ci
```

Run the following in **separate terminals**, also from the repository root:

```sh
go run ./cmd/server -dev-http -origin http://localhost:5173
```

```sh
npm --prefix web run dev
```

Open **http://localhost:5173**. Keep that hostname and port consistent. Vite
proxies `/api/v1` HTTP and WebSocket requests to `127.0.0.1:8080` and preserves
Origin; port 5173 must be free. No CORS override is needed. The development cookie
is HttpOnly and SameSite Strict; production requires HTTPS and Secure cookies.
The development server is for local operation, not production hosting.

Create a room and copy its invitation, then join using another browser profile
or private window for a separate player. Tabs in one profile share an identity,
seat, and readiness. You can stay unready as admin and start with 2–24 other ready
players. Explicit/random seat order and first-bidder choices are independent.

Reload restores membership without joining again. Closing the final tab starts
the server's 60-second reconnect grace after it detects the lost connection.
Explicit leave affects all tabs and withdraws an active participant; it requires
confirmation during play. An expired membership requires an explicit join;
joining during a game makes you a spectator. Server restart loses rooms and
sessions. Names cannot be changed within existing membership.

## Checks

```sh
npm --prefix web run typecheck
npm --prefix web test
npm --prefix web run build
npm --prefix web run format:check
```

Unit/component tests cover inputs, admin permissions, ready-roster changes,
confirmation, session bootstrap, lifecycle cleanup, snapshot ordering, terminal
errors, and bounded retries with original command IDs/envelopes.

Install Chromium once (repeat after Playwright upgrades):

```sh
npx --prefix web playwright install chromium
```

On Linux, browser system dependencies may also be needed:

```sh
npx --prefix web playwright install --with-deps chromium
```

Then stop any manually started servers on ports 8080 and 5173 and run:

```sh
npm --prefix web run test:e2e
```

Playwright owns local Go/Vite servers and shuts them down afterward. Tests cover
real cookies/HTTP/WebSockets, independent sessions, concurrent first-time tabs,
reload restoration, actual WebSocket loss/reconnect, spectator-admin starts,
explicit seat choices, a complete bid/challenge/next-round cycle, spectator
hold-to-peek, leave confirmation, and explicit expired-session recovery across
tabs. A separate mocked protocol fixture checks 24-player layouts at
320/390/1280px; it is not a substitute for the real-network scenarios. Screenshots
are written under `web/test-results`, with traces/screenshots retained on failure.
Full-game recovery, lost-outcome browser scenarios, and real server-grace expiry
are extended in phase 4.3. Browser clock changes cannot advance Go server time.

Inspect phone and desktop layouts manually, including long names, admin ordering,
keyboard focus, and phone form entry. Browser viewport emulation does not replace
checking a physical phone's software keyboard and touch behavior.

## Design

`src/api.ts` handles same-origin HTTP, coordinated session bootstrap, and keyed
HTTP intent. `src/connection.ts` owns one room subscription, reconnect timers,
recipient snapshots, and pending ready/start commands. React components render
that state and maintain local form choices. No global state library or client
implementation of game resolution is needed.

A Web Lock serializes initial anonymous session requests across tabs. A shared
promise also handles duplicate initialization within a tab, including React
Strict Mode. A non-secret localStorage expiry flag also prevents another tab
from silently replacing an expired session after the server clears its cookie;
explicit recovery clears that flag. Credentials remain exclusively in HttpOnly
cookies. Cleanup closes subscriptions and cancels timers, never leaves rooms.
Native WebSocket failures hide HTTP status, so reconnect probes authenticated
room information before attaching. Unavailable membership never silently rejoins.

Counters remain decimal strings on the wire and use BigInt comparisons. Acks do
not overwrite snapshots. A pending command retains its full envelope and is
retried only within a conservative four-minute monotonic client window (server retention
is five minutes). Stale rejections refresh state and require user review. HTTP
network errors expose a retry of the original keyed request instead of generating
another mutation. Uncertain intents live only in memory and are not automatically
replayed after reload.

The UI displays only your connection state. Other players' presence and exact
server reconnect deadlines are not exposed by v1. Private hands are not rendered
by this lobby increment.

`.github/workflows/frontend-checks.yml` adds separate frontend and Chromium jobs
alongside existing Go CI, with pinned Node and locked dependencies. CI starts no
external services and needs no application credentials. Failure artifacts aid
review; no deployment is performed.
