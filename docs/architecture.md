# False Poker - Architecture

## Overview

False Poker is a real-time multiplayer card game.

## Architecture Style

Modular monolith.

The backend is responsible for:
- Managing rooms and players
- Managing game state
- Validating player actions
- Evaluating poker combinations
- Broadcasting game updates
- Shuffling and dealing cards, including all requested random starter and seat
  selections; clients submit choices rather than performing these operations

## Backend

Language: Go

Main modules:
- Game Engine: pure gameplay and domain logic
- Room Manager: active multiplayer sessions
- Transport: HTTP and WebSocket interfaces
- Storage: in-memory persistence for MVP

## Frontend

- React + TypeScript
- Vite
- Responsive UI
- PWA support planned

The frontend displays server-provided state and
submits player actions.

It does not decide the outcome of game actions.

## Communication

HTTP:
- Create room
- Join room
- Get room information

WebSocket:
- Submit gameplay commands
- Receive game state updates
- Receive round results
- Reconnect and synchronize state

## Concurrency

Each room owns its mutable game state.

Commands must be processed sequentially within
a room. Different rooms can run concurrently.

Use a **mutex per room** for the MVP. The room registry protects its lookup map
separately and does not duplicate ownership of game state. Serialize joins,
readiness, game commands, departures, grace-period expirations, and snapshot reads
through the room's synchronization boundary.

Within that boundary, validate a command, mutate state, assign its revision, and
enqueue immutable recipient-specific notifications in revision order. Enqueues
are nonblocking and outgoing queues are bounded. A connection whose queue is full
is closed so it can reconnect and resynchronize; other connections continue.
Socket writes run outside the room lock through one serialized writer per socket.

The in-process room API assigns revision 1 at creation and increments it once per
accepted state change, including lobby changes. Rejected operations, unchanged
readiness, reads, and subscription setup/closure do not increment it. Revisions
continue across games within one live room; they are not durable or global IDs.
Both lobby snapshots and recipient views carry the room revision. Gameplay
commands identify the observed game and round, and reject stale context before
mutating state. Protocol-level request IDs, retry semantics, and expected-revision
checks are specified in phase 3.

`Room.Subscribe` registers one connection and queues its initial recipient view
under the same lock, preventing a missed update between snapshot and subscription.
Each connection owns a queue of 16 detached views, including the initial view.
Overflow closes that subscription with `ErrSlowSubscriber` and discards its
backlog; the transport must observe `Done` and close the socket outside the lock.
Permanent departure closes all subscriptions for that member; room deletion
closes the remaining subscriptions. Closing one subscription alone does not
remove membership or withdraw a player. Connection counting and reconnect grace
are implemented with the transport lifecycle in phase 3.

## Anonymous sessions and connections

Use server-issued opaque anonymous credentials in an HttpOnly, same-origin cookie
with SameSite protection and Secure enabled for HTTPS. A room code is an
invitation, not proof of ownership of another player's identity. Never expose
credentials in room snapshots or invitation links.

Keep room membership, game participation, and connection identity separate. One
player may have several simultaneous authenticated connections. All receive the
same authorized player view and submit commands as the same player. Room
serialization and protocol request IDs/revisions handle conflicting or retried
commands; opening a tab does not create another seat or replace existing tabs.

Start the 60-second reconnect grace period only when the player's last connection
closes. Reconnecting cancels that expiry; stale timer callbacks must check the
current disconnected state and timer generation before withdrawing a player.

Build views according to game role, independently of admin privileges. Active
players cannot inspect opponents' hands; all spectators, including eliminated
players, may inspect surviving players' hands. Spectator clients hide these hands
by default and display them only while a peek control is held. This display choice
does not grant active players access. Reevaluate authorization for each game and
role change, including when a spectator participates in the next game.

Room information is accessible only to authenticated room members. A room code
supports joining without a pre-join preview; password protection is beyond MVP.
Admin authorization is independent of game participation. Elimination does not
transfer the admin role; permanent departure transfers it to a randomly selected
remaining member.

## Persistence

MVP:
- Active rooms stored in memory
- No user accounts
- No database requirement
- Reconnect recovers only live in-memory state; process restart loses rooms,
  sessions, and active games
- On shutdown, stop accepting commands/connections, close sockets, and release
  room timers/subscriptions. Shutdown does not persist games

Delete a room immediately when its last member leaves or their disconnect grace
expires, releasing its timers, subscriptions, and registry entry. Spectators and
members within reconnect grace count as members. Rooms with members remain alive
without an inactivity or finished-game deadline. A finished game clears readiness
and permits another admin-started game in the same room, with fresh game state,
hands, starter/seat configuration, and game identity. Keep transport membership
and active connections; previous game commands cannot act on the new game.

Future:
- PostgreSQL for persistent data

## Deployment

- Docker Compose
- Single backend instance initially
- HTTPS/WSS behind a reverse proxy

## Architectural Constraints

- Game Engine must not depend on HTTP or WebSocket.
- Active players cannot access opponents' current hands before resolution.
  All spectators are explicitly authorized to inspect survivors' hands;
  admin privileges alone do not grant hand visibility.
- All gameplay commands are validated server-side.
- Horizontal scaling is outside MVP scope.

