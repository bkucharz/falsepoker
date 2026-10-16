# False Poker - Requirements

## Product

A browser-based multiplayer bluffing card game.

## MVP Features

- Create a private game room
- Join a room using a code or invitation link
- Play without registration
- Choose a display name
- The room creator becomes the sole room admin immediately; only the admin can
  start a game. Granting additional admin roles is deferred beyond the MVP
- Start a game with 2–24 ready participants; other room members are spectators.
  The admin can start without participating
- Admin selects or randomly shuffles seat order and selects or randomly chooses
  the first bidder before play
- Receive private cards
- Place bids during a turn
- Challenge the previous bid
- Reveal cards and resolve the round
- The next starting player advances the revealed round with “Next round”
- Continue until the winning player is determined
- Reconnect after temporarily losing connection
- Join an ongoing game as a spectator without becoming a participant
- All spectators, including eliminated players, may inspect survivors' current
  hands only while holding a peek control; hands are hidden again on release
- Use multiple simultaneous tabs/connections for the same player
- Play another game in the same room by declaring readiness again

## Non-Goals for MVP

- Public matchmaking
- User accounts
- Rankings
- In-game chat
- Computer-controlled opponents
- Native mobile apps
- Multiple backend instances
- Password-protected rooms (possible future addition)

## Quality Requirements

- Game state must be authoritative on the server.
- Active players cannot see other players' current hands before resolution.
  All spectators are authorized to inspect survivors' current hands, including
  new-round hands, using a hold-to-peek control. The UI hides these hands by default
  and on release or interruption of the hold.
  Resolved-round cards are revealed publicly within the room.
- Invalid and out-of-turn moves must be rejected.
- Domain logic must be covered by automated tests.
- The application must run locally without
  external cloud services.

## Room and connection lifecycle

- Admin privileges are independent of game participation and elimination. An
  eliminated admin retains the role. When the sole admin leaves explicitly or
  their disconnect grace expires, the server randomly assigns the role to one
  remaining room member, including spectators. Temporary disconnect alone does
  not transfer the role.
- A player has 60 seconds to reconnect after losing all their connections. Play
  continues during this grace period, but their turn is not skipped. If play
  reaches their turn, it waits for reconnect or expiry.
- A newly created/joined membership has the same 60 seconds to open its first
  game connection. Abandoned HTTP joins then follow normal departure handling;
  repeated HTTP requests do not extend the attachment window.
- Explicitly leaving or exhausting the grace period withdraws a player from
  participation. Their turn is skipped, but their dealt cards continue to count
  for the current round. They receive no cards in subsequent rounds.
- A departed player's last bid remains challengeable. A false claim records that
  departed bidder as the loser without another penalty; a true claim penalizes
  the challenger normally. If the next starting player departs, the next remaining
  participant in seat order takes over the “Next round” action and opening bid.
- If departure leaves one participant, they win immediately; no further round
  resolution or deal is needed.
- There is no turn time limit for the MVP, including the “Next round” action.
- Multiple tabs represent the same player, seat, readiness state, and hand; they
  do not create additional participants.
- Active state is in memory. Reconnect restores a live room, but server restart
  loses active rooms and games.
- Delete a room immediately when its last member leaves, including spectators.
  Disconnected members still count during their 60-second grace period; expiry
  removes them. There is no idle-lobby or finished-room expiry while members remain.
- After a game ends, clear readiness. Members may declare readiness again, and the
  current admin may start a new game with 2–24 ready participants. Previous
  elimination and hand-peeking permissions do not carry into a new participant role.
- A room code is used solely to join in the MVP. Do not provide a pre-join room
  preview or member list; room information requires authenticated membership.

