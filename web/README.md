# Browser client

The browser client uses React, TypeScript, and Vite. In local development Vite
proxies HTTP and WebSocket requests to the Go server.

## Run locally

Use Go 1.27.1, Node.js 22.23.1 (from `.node-version`), and npm 10.9.8. From the
repository root, install dependencies and start the backend and frontend in
separate terminals:

```sh
npm --prefix web ci
```

```sh
go run ./cmd/server -dev-http -origin http://localhost:5173
```

```sh
npm --prefix web run dev
```

Open <http://localhost:5173>. Use another browser profile or a private window
for another player; tabs in one profile share an identity and seat. For phone
testing, connect the phone and computer to the same network, then open the
computer's local IP address on the phone.

## Checks

Run from the repository root:

```sh
npm --prefix web run format:check
npm --prefix web run typecheck
npm --prefix web test
npm --prefix web run build
npm --prefix web run test:e2e
```

Install Playwright's Chromium browser once:

```sh
npx --prefix web playwright install chromium
```

On Linux, install its system dependencies with:

```sh
npx --prefix web playwright install --with-deps chromium
```

The browser suite starts its own Go and Vite servers and covers multiplayer,
reconnect, spectator visibility, and complete-game flows. It needs no
credentials or external services. See [CI documentation](../docs/ci.md).

## Limitations

Browser emulation does not replace checking touch and software-keyboard behavior
on a physical phone. Restarting the server clears rooms and active games. For
architecture and protocol details, see [architecture](../docs/architecture.md)
and [protocol](../docs/protocol.md).
