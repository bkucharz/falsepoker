# False Poker - Agent Instructions

## Project Overview

False Poker is a real-time multiplayer card game.

## Technology

- Backend: Go
- Frontend: React, TypeScript, Vite
- Communication: HTTP and WebSockets
- Active game state: in memory
- Future persistence: PostgreSQL
- Deployment: Docker Compose

## Architecture

- Use a modular monolith.
- The server is authoritative over all game state.
- Keep game rules independent of networking,
  persistence, and UI code.
- Favor Go's standard library where practical.
- Avoid unnecessary abstractions and dependencies.
- Do not introduce microservices or distributed
  infrastructure without a concrete requirement.

## Project Documentation

Read the relevant documents when a task requires them:

- docs/requirements.md: product scope and features
- docs/game-rules.md: game mechanics and validation
- docs/architecture.md: architectural decisions
- docs/protocol.md: network message contracts,
  when available

If a rule is unspecified, do not silently invent it.
Explain the ambiguity before implementing behavior
that depends on it.

## Development Guidelines

- Prefer simple, idiomatic, maintainable code.
- Follow established conventions in the repository.
- Keep changes focused on the requested task.
- Do not perform unrelated refactoring.
- Handle errors explicitly.
- Avoid premature optimization.
- Keep dependencies minimal.

## Testing

- Add or update tests for changed behavior.
- Prefer table-driven tests for Go domain logic.
- Use deterministic randomness in tests.
- Run relevant tests before reporting completion.
- Use Go's race detector for concurrency-related
  changes.
- Never claim tests passed unless they were run.

## Workflow

- For significant features, propose a short
  implementation plan before making changes.
- Explain important design tradeoffs.
- Do not modify established architecture without
  explaining why.
- Do not commit changes unless explicitly requested.
- After implementation, summarize changed files,
  verification performed, and remaining limitations.

