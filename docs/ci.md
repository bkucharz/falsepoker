# Continuous integration

GitHub Actions runs the same checks contributors can run locally. Both workflows
run for pull requests targeting `main`, pushes to `main`, and manual dispatches.
They use read-only repository permissions, cancel superseded runs, and do not
deploy or require application credentials.

## Go checks

`.github/workflows/go-checks.yml` reads the Go version from `go.mod`, checks
tracked Go files with `gofmt`, verifies module metadata with `go mod tidy -diff`,
runs `go vet ./...`, and executes `go test -race -count=1 -cover ./...`.
CGO is enabled because Go's race detector requires it on the Linux runner.

## Frontend and browser checks

`.github/workflows/frontend-checks.yml` has two jobs:

- **Frontend checks** installs the exact lockfile dependency graph with `npm ci`,
  checks Prettier and TypeScript, runs unit/component tests, and builds the site.
- **Browser integration** installs Go and the lockfile dependencies, installs
  Chromium and its Linux packages, then runs Playwright. Playwright starts local
  Go and Vite servers. On failure the workflow uploads Playwright traces,
  screenshots, and reports for seven days.

The toolchain sources are `go.mod` (Go 1.27.1), `web/.node-version` (Node
22.23.1), and the `npm` version documented in `web/README.md` (10.9.8). Frontend
dependencies are pinned by `web/package-lock.json`; CI uses `npm ci` so it fails
if the manifest and lockfile disagree.

## Run CI checks locally

From the repository root:

```sh
gofmt -l cmd internal
go mod tidy -diff
go vet ./...
go test -race -count=1 -cover ./...
npm --prefix web ci
npm --prefix web run format:check
npm --prefix web run typecheck
npm --prefix web test
npm --prefix web run build
npx --prefix web playwright install --with-deps chromium
npm --prefix web run test:e2e
git diff --check
```

No workflow publishes artifacts to production. Hosting, TLS termination, and
deployment are deferred until there is a concrete demo or hosting requirement.
