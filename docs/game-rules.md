# False Poker — Game Rules

## 1. Overview

False Poker is a multiplayer bluffing card game inspired by poker. Players make progressively higher claims about poker combinations that exist **across all cards dealt to all players**, not necessarily within any one player's hand.

A player sees their own cards but does not see the other players' cards during bidding. On their turn, they must either raise the current claim or challenge it. The last player remaining wins the game.

## 2. Deck and players

- Use one **24-card deck**, with four suits and six ranks: **9, 10, J, Q, K, A**.
- There are no duplicate physical cards in the deck.
- A game starts with **2–24 players**, subject to the elimination-card limit in §5.
- Each participating player starts the game with **one card**.

The room creator is immediately the **sole room admin** for the MVP, and only
the admin may start a game. A room may contain more people than the game:
**2–24 ready players** participate, while all other room members spectate. The
admin need not participate. People joining after the start are spectators.

Before play, the admin chooses the seat order explicitly or asks the server to shuffle it
randomly. This order remains fixed for the game. Turns wrap around the seat order,
skipping eliminated or withdrawn players. Starter selection and seat ordering
apply only to the game's participants.

All spectators, including eliminated players and late joiners, may inspect
survivors' current hands by holding a peek button or equivalent control. Hands are
hidden by default and hidden again when the hold ends or is interrupted.
Spectators cannot bid or challenge. Admin privileges are independent of game
participation: an eliminated admin retains the role. If the admin permanently
leaves the room, the server randomly chooses a remaining member as the new admin.

## 3. Rounds

### 3.1 Initial round

1. The admin selects the first bidder explicitly or requests random selection by
   the server from the participating players.
2. The server calculates the round's elimination limit, shuffles the deck, and
   deals one card to each player.
3. The selected starting player makes the first bid. Selecting the first bidder
   does not change the seat order.

### 3.2 Bidding

1. Players act in turn.
2. The first player in a round announces a poker-combination claim (a **bid**).
3. Each subsequent player must choose one of the following:
   - **Raise:** announce a bid strictly higher than the previous bid, using the ordering in §6.
   - **Challenge:** demand that the previous bid be verified against the union of all cards dealt to all players in the current round.
4. Players base their decisions on their private cards and the public bids made by other players.
5. Bidding continues until someone challenges.

A bid specifies its combination category and the category-specific parameters described in §6. Its claimed combination need not consist entirely of cards in the bidder's hand.

### 3.3 Resolving a challenge

Reveal the **complete set of currently dealt cards** and determine whether the last bid is satisfied:

- **If the claimed combination exists:** the **challenger** loses the round.
- **If the claimed combination does not exist:** the **player who made the last bid** loses the round.

Only the losing player receives a card-count penalty for that round, unless they
have already withdrawn (see §7); a departed loser receives no additional penalty.

The server resolves the challenge once, applies the penalty or elimination using
the limit from the round just played, and retains the revealed cards and result
for the resolution phase. Bidding is not allowed during this phase.

### 3.4 Starting the next round

If the loser survives, their required hand size increases by **one card**, and
they become the next starting player. If the loser is eliminated, the **next
surviving player after them in seat order** becomes the next starting player.
If that starting player permanently departs before advancing the round, the next
remaining participant after them in seat order takes over. A temporary disconnect
waits for reconnect or the 60-second grace expiry before passing responsibility.

If more than one player survives, the game stays in the resolution phase until
that next starting player clicks **“Next round”**. Only that player may advance
the round; being the admin does not override this rule. The round does not advance
automatically.

On this action, the server recalculates the elimination limit from the surviving
player count, collects all cards, shuffles the full deck, and deals each survivor
their required hand size. The next starting player then makes the opening bid.
Players perform no physical shuffling or dealing in the application.

Thus, hand sizes persist between rounds, while the cards are reshuffled and
redealt. Players do not retain their previous card identities. New-round hands
remain hidden from other active players, while all spectators may inspect them
using the hold-to-peek control. The previous round's result and cards remain revealed.

## 4. Winning

The game ends immediately when only one player remains. That player wins. The
final round's cards and result remain revealed; there is no “Next round” action,
new deal, or further bidding after the game ends.

An explicit departure or disconnect-grace expiry leaving one participant also
ends the game immediately with that participant as winner; do not wait for a
challenge or “Next round”. Departures are processed sequentially, so the transition
to one remaining participant finishes the game before any subsequent departure.

After the game, room members may declare readiness again. Readiness is cleared at
game end; the admin may start another game with 2–24 ready participants, choosing
the new seat order and first bidder as in §3.1. All participants start with one
card, regardless of previous penalties or elimination. A returning participant
cannot retain spectator access to opponents' hands during the new game.

## 5. Elimination and card limit

A player is eliminated upon losing a round when adding one card would reach the
round's elimination limit. The limit is an **exclusive threshold**, never a hand
size dealt to a surviving player. For instance, at a limit of **6**, a player
holding **5 cards** who loses is eliminated rather than continuing with 6 cards.

The standard limit is **6 cards** for games with up to four players.

For larger games, reduce the limit so that the maximum possible total of
surviving players' hand sizes fits the deck:

\[
N \times (L - 1) \le 24
\]

where `N` is the current surviving player count and `L` is the common
elimination-card limit. Each survivor can hold at most `L - 1` cards.

Before each round, calculate:

```text
eliminationLimit = min(6, floor(24 / playerCount) + 1)
```

Keep this limit unchanged throughout that round and use it to resolve its loss.
Remove any eliminated player before calculating the next round's limit. A new
limit does not undo an elimination from the previous round.

Examples:

| Players | Elimination limit | Maximum hand size | Maximum dealt cards |
| ---: | ---: | ---: | ---: |
| 2 | 6 | 5 | 10 |
| 3 | 6 | 5 | 15 |
| 4 | 6 | 5 | 20 |
| 5 | 5 | 4 | 20 |
| 6 | 5 | 4 | 24 |
| 7 | 4 | 3 | 21 |
| 8 | 4 | 3 | 24 |
| 9 | 3 | 2 | 18 |
| 12 | 3 | 2 | 24 |
| 13 | 2 | 1 | 13 |
| 24 | 2 | 1 | 24 |

With **13–24 survivors**, the limit is **2**. Every player holds one card, so a
loss eliminates them. At **12 survivors**, the limit becomes **3**, and a player
can survive a loss by increasing their hand from one card to two.

**Limit-change example:** With five survivors, the round's limit is 5. A player
holding four cards loses and is eliminated using that limit. Four survivors
remain, so the next round uses limit 6. Their required hand sizes stay unchanged;
the eliminated player is not restored by the higher limit.

**Minimum-player and terminal example:** A two-player game uses limit 6 and starts
with one card each. A loser holding fewer than five cards receives one more card.
When a player holding five cards loses, they are eliminated and the other player
wins immediately; no further round is created.

## 6. Bid ordering

### 6.1 Categories (highest to lowest)

| Priority | Combination | Required parameters |
| ---: | --- | --- |
| 1 | Royal straight flush  | Suit |
| 2 | Straight flush  | Suit |
| 3 | Four of a kind  | Rank |
| 4 | Flush  | Suit |
| 5 | Full house  | Rank of triple, rank of pair |
| 6 | Three of a kind  | Rank |
| 7 | High straight  | None |
| 8 | Low straight  | None |
| 9 | Two pairs  | Higher pair rank, lower pair rank |
| 10 | Pair  | Rank |
| 11 | High card  | Rank |

This category hierarchy is specific to the described game and should not be replaced with standard five-card poker hand rankings. In particular, **flush ranks above full house** here.

There is no suit precedence.

- **Low straight:** 9, 10, J, Q, K, regardless of suit.
- **High straight:** 10, J, Q, K, A, regardless of suit.
- **Flush:** any five cards of the named suit; their ranks need not be consecutive.
- **Straight flush:** 9, 10, J, Q, K, all in the named suit.
- **Royal straight flush:** 10, J, Q, K, A, all in the named suit. This category
  ranks higher than straight flush.

### 6.2 Comparing bids

A new bid is higher than the previous bid if:

1. Its **category** has higher priority; or
2. Both bids have the same category, but the new bid has a higher **primary parameter**; or
3. Both category and primary parameter are equal, but the new bid has a higher **secondary parameter**.

There is no suit precedence. Changing only the suit does not constitute a raise.
Every flush category requires an explicit suit, but that suit is ignored when
comparing bids. A hearts flush cannot raise a clubs flush in the same category.

For a **full house**, the primary parameter is the rank of the three-of-a-kind; the secondary parameter is the rank of the pair.

The triple and pair must have **different ranks**. Three aces and two aces is not
a valid full-house claim; only four aces exist in the deck.

For **two pairs**, the primary parameter is the higher of the two pair ranks, and the secondary parameter is the lower pair rank.

The pair ranks must be **different** and are stated higher first. Rank parameters
must be one of 9, 10, J, Q, K, A, ordered from 9 lowest to A highest. A bid includes
only the parameters required by its category; missing, extra, or invalid
parameters make it invalid.

Examples :

- Pair of aces > pair of kings.
- Two pairs (aces and nines) > two pairs (kings and queens).
- Full house (three kings, two tens) > full house (three queens, two aces).
- Any four of a kind > any flush because category takes precedence over parameters.


### 6.3 Checking whether a bid exists

A challenge evaluates the **union of all dealt cards**. The bid is successful if the named category and its stated parameters can be formed using cards from that union, even if the necessary cards are held by different players.

For example, a claim of a pair of kings succeeds if at least two kings occur among all dealt cards, regardless of who holds them.

A stronger, different category being available does not by itself change what the player claimed: evaluate the **specific claim and parameters**.

For every flush category, the claimed combination must exist in the **named
suit**, even though suits do not determine bid ordering.

A flush claim succeeds when the dealt cards contain **at least five cards of the
named suit**: the five-card combination can be selected from that union. All six
cards of a suit therefore also satisfy a flush claim. For example, 9, 10, J, Q, A
of hearts satisfy a hearts flush even though they are not consecutive. Four
hearts do not satisfy it, regardless of cards in other suits.

## 7. Disconnects and departures

A participant losing all connections has **60 seconds** to reconnect as the same
player. Their seat and hand remain unchanged during that grace period. Play can
continue until their turn, but that turn is not skipped while the grace period
lasts. Reconnecting one tab is sufficient; another tab closing does not disconnect
a player who still has a live connection.

Explicit departure or expiry withdraws the participant: skip their turn, retain
their cards in the union used to resolve the current round, and exclude them from
all subsequent deals. Do not recalculate the current round's elimination limit
because of a departure. A departed player's last bid remains challengeable. If
that claim is true, the challenger loses and receives the usual penalty. If false,
the departed bidder is recorded as the round loser, with no further hand-size
penalty or restoration to participation. The next remaining participant after the
departed loser in seat order becomes the next starting player.

For example, A bids and leaves while B and C remain. A's cards still count. B can
challenge A's bid: a true claim penalizes B; a false claim records A as loser
without penalizing B or C. If departures leave just one participant, the immediate
win in §4 takes precedence over continuing this round.

There is **no turn timeout** in the MVP. A connected player is not penalized for
taking time to bid, challenge, or click “Next round”.

## 8. Implementation invariants

- Each physical card belongs to at most one player's current hand or the undealt deck.
- Every active player has exactly their current required number of cards at the beginning of a round.
- A non-starting bid must be strictly higher than the previous bid.
- A challenge resolves only the immediately preceding bid.
- Each resolved challenge identifies exactly one loser. Only that player receives
  a penalty, and an already withdrawn loser receives no additional penalty.
- Eliminated players receive no cards and take no turns in later rounds.
- The winner is the sole surviving player.
- Active players never receive another player's current hand before resolution;
  all spectators are authorized to inspect survivors' current hands through the
  hold-to-peek control.
