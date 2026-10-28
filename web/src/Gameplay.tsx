import { useEffect, useRef, useState } from 'react';
import { Settings } from 'lucide-react';
import type {
  CSSProperties,
  KeyboardEvent as ReactKeyboardEvent,
  PointerEvent as ReactPointerEvent,
  RefObject,
} from 'react';
import type {
  Bid,
  BidCategory,
  Card,
  Game,
  PlacedBid,
  RoomState,
  RoundResult,
} from './protocol';
import type { ConnectionStatus, RoomConnection } from './connection';

const ranks = ['9', '10', 'J', 'Q', 'K', 'A'];
const suits = [
  ['clubs', 'Clubs', '♣'],
  ['diamonds', 'Diamonds', '♦'],
  ['hearts', 'Hearts', '♥'],
  ['spades', 'Spades', '♠'],
] as const;
const categories: { value: BidCategory; label: string; fields: string[] }[] = [
  { value: 'high_card', label: 'High card', fields: ['rank'] },
  { value: 'pair', label: 'Pair', fields: ['rank'] },
  { value: 'two_pairs', label: 'Two pairs', fields: ['two-ranks'] },
  { value: 'low_straight', label: 'Low straight (9–K)', fields: [] },
  { value: 'high_straight', label: 'High straight (10–A)', fields: [] },
  { value: 'three_of_a_kind', label: 'Three of a kind', fields: ['rank'] },
  { value: 'full_house', label: 'Full house', fields: ['two-ranks'] },
  { value: 'flush', label: 'Flush', fields: ['suit'] },
  { value: 'four_of_a_kind', label: 'Four of a kind', fields: ['rank'] },
  { value: 'straight_flush', label: 'Straight flush (9–K)', fields: ['suit'] },
  {
    value: 'royal_straight_flush',
    label: 'Royal straight flush (10–A)',
    fields: ['suit'],
  },
];
const categoryLabels = new Map(
  categories.map((item) => [item.value, item.label]),
);

interface PeekState {
  context: string;
  ids: string[];
}

export function Gameplay({
  state,
  connected = true,
  connectionStatus,
  pending,
  error,
  errorCode,
  command,
  onLeave,
  onExitGame,
}: {
  state: RoomState;
  connected?: boolean;
  connectionStatus?: ConnectionStatus;
  pending: boolean;
  error?: string | null;
  errorCode?: string | null;
  command: RoomConnection['command'];
  onLeave?: () => void;
  onExitGame?: () => void;
}) {
  const game = state.game!;
  const liveStatus =
    connectionStatus ?? (connected ? 'connected' : 'reconnecting');
  const context = `${state.game_id}:${game.round_id}:${state.role}:${state.revision}`;
  const [peek, setPeek] = useState<PeekState>({ context, ids: [] });
  const [category, setCategory] = useState<BidCategory>('high_card');
  const [primaryRank, setPrimaryRank] = useState('9');
  const [secondaryRank, setSecondaryRank] = useState('10');
  const [suit, setSuit] = useState('clubs');
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [bidOpen, setBidOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState(false);
  const [viewport, setViewport] = useState(() => ({
    width: window.innerWidth,
    height: window.innerHeight,
  }));
  const settingsDialog = useRef<HTMLDialogElement>(null);
  const bidDialog = useRef<HTMLDialogElement>(null);
  const peekIDs = peek.context === context ? peek.ids : [];

  useEffect(() => {
    setPeek({ context, ids: [] });
    const clear = () => setPeek({ context, ids: [] });
    const visibility = () => {
      if (document.visibilityState !== 'visible') clear();
    };
    window.addEventListener('blur', clear);
    document.addEventListener('visibilitychange', visibility);
    return () => {
      window.removeEventListener('blur', clear);
      document.removeEventListener('visibilitychange', visibility);
    };
  }, [context]);

  useEffect(() => {
    const resize = () =>
      setViewport({ width: window.innerWidth, height: window.innerHeight });
    window.addEventListener('resize', resize);
    return () => window.removeEventListener('resize', resize);
  }, []);

  useEffect(() => {
    const dialog = settingsDialog.current;
    if (!dialog) return;
    if (settingsOpen && !dialog.open) {
      if (dialog.showModal) dialog.showModal();
      else dialog.setAttribute('open', '');
    }
    if (!settingsOpen && dialog.open) {
      if (dialog.close) dialog.close();
      else dialog.removeAttribute('open');
    }
  }, [settingsOpen]);

  useEffect(() => {
    const dialog = bidDialog.current;
    if (!dialog) return;
    if (bidOpen && !dialog.open) {
      if (dialog.showModal) dialog.showModal();
      else dialog.setAttribute('open', '');
    }
    if (!bidOpen && dialog.open) {
      if (dialog.close) dialog.close();
      else dialog.removeAttribute('open');
    }
  }, [bidOpen]);

  const playerName = (id: string | null | undefined) =>
    game.players.find((player) => player.player_id === id)?.display_name ??
    state.members.find((member) => member.player_id === id)?.display_name ??
    'A player';
  const currentPlayer = playerName(game.current_player_id);
  const myPlayer =
    state.role === 'participant'
      ? game.players.find((player) => player.player_id === state.recipient_id)
      : undefined;
  const myTurn =
    state.role === 'participant' &&
    game.phase === 'playing' &&
    game.round_phase === 'bidding' &&
    game.current_player_id === state.recipient_id;
  const nextRound =
    game.phase === 'playing' &&
    game.round_phase === 'resolution' &&
    game.next_starter_id === state.recipient_id;
  const canBid = myTurn && connected && !pending;
  const canPeek = state.role === 'spectator' && game.round_phase === 'bidding';
  const publicHands = game.round_phase === 'resolution';
  const result = game.last_result;
  const resultShowsCurrentHands =
    publicHands && result?.round_id === game.round_id;
  const chosen = categories.find((item) => item.value === category)!;
  const bid: Bid = { category };
  if (chosen.fields.includes('rank')) bid.primary_rank = primaryRank;
  if (chosen.fields.includes('two-ranks')) {
    bid.primary_rank = primaryRank;
    bid.secondary_rank = secondaryRank;
  }
  if (chosen.fields.includes('suit')) bid.suit = suit;

  const setHeld = (id: string, held: boolean) => {
    setPeek((current) => {
      const ids = current.context === context ? current.ids : [];
      return {
        context,
        ids: held
          ? ids.includes(id)
            ? ids
            : [...ids, id]
          : ids.filter((item) => item !== id),
      };
    });
  };
  const handVisible = (playerID: string) =>
    publicHands ||
    (state.role === 'participant' && playerID === state.recipient_id) ||
    (canPeek && (peekIDs.includes('*') || peekIDs.includes(playerID)));
  const opponentCount = game.players.length - (myPlayer ? 1 : 0);
  const tablePerimeter =
    2 *
    (Math.max(260, viewport.width * 0.86) +
      Math.max(160, viewport.height * 0.5));
  const seatWidth = Math.max(
    44,
    Math.min(132, tablePerimeter / Math.max(1, opponentCount)),
  );
  const selfSeat = myPlayer ? game.players.indexOf(myPlayer) : -1;
  const winnerName = game.winner_id ? playerName(game.winner_id) : null;
  const invitation = `${location.origin}/room/${state.room_code}`;
  const copyInvite = async () => {
    try {
      await navigator.clipboard.writeText(invitation);
      setCopied(true);
      setCopyError(false);
    } catch {
      setCopyError(true);
    }
  };
  const startBid = () => {
    if (canBid) setBidOpen(true);
  };
  const submitBid = () => {
    command('bid', { bid });
    setBidOpen(false);
  };

  return (
    <section className="gameplay" aria-label="False Poker game table">
      <header className="table-topbar">
        <div className="table-brand" aria-label="False Poker">
          <span aria-hidden="true">♠</span> FALSE POKER
        </div>
        <div className="table-top-status">
          <span className={`connection-dot ${connected ? 'online' : ''}`} />
          {liveStatus === 'connected'
            ? 'Connected'
            : liveStatus === 'offline'
              ? 'Offline'
              : 'Reconnecting'}
        </div>
        <button
          type="button"
          className="settings-trigger"
          aria-label="Open game settings"
          onClick={() => setSettingsOpen(true)}
        >
          <Settings aria-hidden="true" size={18} />
        </button>
      </header>

      {error && (
        <div className="game-error" role="alert">
          <span>{error}</span>
          {errorCode?.startsWith('STALE_') && (
            <span>Review your choices before trying again.</span>
          )}
        </div>
      )}

      <div className="felt-table" aria-label={`Round ${game.round_id} table`}>
        <div className="table-grain" aria-hidden="true" />
        <ol className="opponent-seats" aria-label="Opponents around the table">
          {game.players.map((player, index) => {
            if (player.player_id === myPlayer?.player_id) return null;
            const active = !player.eliminated && !player.withdrawn;
            const delta =
              selfSeat < 0
                ? index + 1
                : (index - selfSeat + game.players.length) %
                  game.players.length;
            const angle =
              Math.PI / 2 + (delta * Math.PI * 2) / game.players.length;
            const seatStyle = {
              left: `${50 + Math.cos(angle) * 43}%`,
              top: `${50 + Math.sin(angle) * 39}%`,
              width: `${seatWidth}px`,
            } as CSSProperties;
            const visible = active && handVisible(player.player_id);
            const backs = active ? Math.min(player.hand_count, 5) : 0;
            return (
              <li
                key={player.player_id}
                className="opponent-seat-position"
                style={seatStyle}
              >
                <OpponentSeat
                  player={player}
                  active={active}
                  current={game.current_player_id === player.player_id}
                  canPeek={canPeek && active}
                  visible={visible}
                  backCount={backs}
                  onHold={setHeld}
                />
              </li>
            );
          })}
        </ol>

        <div className="table-center" aria-live="polite">
          <span className="round-chip">
            {game.phase === 'finished' ? 'GAME OVER' : `ROUND ${game.round_id}`}
          </span>
          {winnerName ? (
            <>
              <h1>{winnerName} wins</h1>
              <p>The game is complete.</p>
            </>
          ) : game.round_phase === 'resolution' && result ? (
            <RoundResultSummary result={result} playerName={playerName} />
          ) : game.round_phase === 'resolution' ? (
            <>
              <h1>Hands revealed</h1>
              <p>The game ended after a player left.</p>
            </>
          ) : (
            <>
              <p className="table-turn-label">
                {myTurn ? 'YOUR TURN' : `${currentPlayer.toUpperCase()} TO ACT`}
              </p>
              {game.last_bid ? (
                <BidCard placed={game.last_bid} playerName={playerName} />
              ) : (
                <h1>First claim</h1>
              )}
              <p className="table-facts">
                {game.elimination_limit}-card limit <span>·</span>{' '}
                {
                  game.players.filter(
                    (player) => !player.eliminated && !player.withdrawn,
                  ).length
                }{' '}
                in the game
              </p>
            </>
          )}
          {game.phase === 'finished' && (
            <button
              className="table-action primary-action finished-exit"
              onClick={onExitGame}
            >
              Exit to room
            </button>
          )}
          {game.round_phase === 'bidding' && result && (
            <p className="previous-result">
              Last round:{' '}
              {result.claim_satisfied ? 'claim true' : 'claim false'} ·{' '}
              {playerName(result.loser_id)} lost
            </p>
          )}
        </div>
      </div>

      <div className="game-actions" aria-label="Available game actions">
        {canPeek && (
          <PeekButton
            id="*"
            label="Hold to peek at all hands"
            onHold={setHeld}
            className="table-peek-button"
          />
        )}
        {myTurn ? (
          <>
            <button
              className="table-action primary-action"
              disabled={!canBid}
              onClick={startBid}
            >
              <span aria-hidden="true">↗</span>
              {pending
                ? 'Sending…'
                : game.last_bid
                  ? 'Raise claim'
                  : 'Make a claim'}
            </button>
            {game.last_bid && (
              <button
                className="table-action challenge-action"
                disabled={!canBid}
                onClick={() => command('challenge', {})}
              >
                Challenge
              </button>
            )}
          </>
        ) : nextRound ? (
          <button
            className="table-action primary-action"
            disabled={!connected || pending}
            onClick={() => command('next_round', {})}
          >
            {pending ? 'Dealing…' : 'Next round'}{' '}
            <span aria-hidden="true">↗</span>
          </button>
        ) : game.phase === 'playing' && game.round_phase === 'resolution' ? (
          <p className="action-status">
            Waiting for {playerName(game.next_starter_id)} to deal the next
            round
          </p>
        ) : game.phase === 'finished' ? null : (
          <p className="action-status">
            {liveStatus === 'offline'
              ? 'Offline — actions are paused.'
              : connected
                ? `Waiting for ${currentPlayer}`
                : 'Reconnecting to the table…'}
          </p>
        )}
      </div>

      <section
        className={`self-seat ${state.role === 'participant' ? '' : 'is-spectator'}`}
        aria-label="Your seat"
      >
        <div className="self-seat-label">
          <div>
            <strong>
              {state.role === 'participant'
                ? 'Your hand'
                : 'Watching the table'}
            </strong>
            <span>
              {state.role === 'participant'
                ? `${myPlayer?.hand_count ?? 0} ${myPlayer?.hand_count === 1 ? 'card' : 'cards'}`
                : 'Hold a player seat to peek'}
            </span>
          </div>
          {myPlayer && (
            <span className="self-player-name">{myPlayer.display_name}</span>
          )}
        </div>
        {state.role === 'participant' && myPlayer ? (
          <div className="self-card-fan" aria-label="Your private hand">
            {(myPlayer.hand ?? []).map((card, index) => (
              <CardFace
                key={`${card.rank}-${card.suit}-${index}`}
                card={card}
                index={index}
                count={myPlayer.hand?.length ?? 0}
              />
            ))}
          </div>
        ) : (
          <div className="spectator-mark" aria-hidden="true">
            ♠
          </div>
        )}
      </section>

      <SettingsDialog
        dialogRef={settingsDialog}
        open={settingsOpen}
        onClose={() => setSettingsOpen(false)}
        state={state}
        connected={connected}
        connectionStatus={liveStatus}
        invitation={invitation}
        copied={copied}
        copyError={copyError}
        onCopy={() => void copyInvite()}
        onLeave={() => {
          setSettingsOpen(false);
          onLeave?.();
        }}
        result={result}
        playerName={playerName}
      />

      <dialog
        className="bid-dialog"
        ref={bidDialog}
        aria-labelledby="bid-dialog-title"
        onCancel={(event) => {
          event.preventDefault();
          setBidOpen(false);
        }}
      >
        <div className="dialog-heading">
          <div>
            <span className="eyebrow">YOUR MOVE</span>
            <h2 id="bid-dialog-title">
              {game.last_bid ? 'Raise the claim' : 'Make the first claim'}
            </h2>
          </div>
          <button
            type="button"
            className="dialog-close"
            aria-label="Close bid form"
            onClick={() => setBidOpen(false)}
          >
            ×
          </button>
        </div>
        <p className="action-help">
          Claims concern all cards dealt this round. Bluffing is allowed.
        </p>
        <BidForm
          disabled={!canBid}
          statusLabel={
            pending
              ? 'Sending…'
              : liveStatus === 'offline'
                ? 'Offline'
                : connected
                  ? 'Place claim'
                  : 'Reconnecting…'
          }
          category={category}
          setCategory={setCategory}
          primaryRank={primaryRank}
          setPrimaryRank={setPrimaryRank}
          secondaryRank={secondaryRank}
          setSecondaryRank={setSecondaryRank}
          suit={suit}
          setSuit={setSuit}
          onSubmit={submitBid}
        />
      </dialog>
    </section>
  );
}

function OpponentSeat({
  player,
  active,
  current,
  canPeek,
  visible,
  backCount,
  onHold,
}: {
  player: Game['players'][number];
  active: boolean;
  current: boolean;
  canPeek: boolean;
  visible: boolean;
  backCount: number;
  onHold: (id: string, held: boolean) => void;
}) {
  const content = (
    <>
      <span className="opponent-name">
        <span className="opponent-avatar" aria-hidden="true">
          {Array.from(player.display_name)[0]?.toUpperCase()}
        </span>
        <span className="opponent-label-text">
          {player.display_name}
          {current && <small>ACTING</small>}
          {!active && <small>{player.withdrawn ? 'LEFT' : 'OUT'}</small>}
        </span>
        {active && <span className="opponent-count">{player.hand_count}</span>}
      </span>
      {visible ? (
        <span
          className="opponent-fan face-up"
          aria-label={`${player.display_name}'s revealed cards`}
        >
          {(player.hand ?? []).slice(0, 5).map((card, index) => (
            <CardFace
              key={`${card.rank}-${card.suit}-${index}`}
              card={card}
              index={index}
              count={Math.min(player.hand_count, 5)}
              small
            />
          ))}
        </span>
      ) : (
        <span className="opponent-fan face-down" aria-hidden="true">
          {Array.from({ length: backCount }, (_, index) => (
            <span
              className="card-back"
              key={index}
              style={fanStyle(index, backCount, 18, 7)}
            />
          ))}
        </span>
      )}
    </>
  );
  const label = `${player.display_name}, ${active ? `${player.hand_count} ${player.hand_count === 1 ? 'card' : 'cards'}` : player.withdrawn ? 'left the game' : 'eliminated'}${current ? ', current turn' : ''}${canPeek ? '. Hold to peek at hand' : ''}`;
  const holdHandlers = usePeekHold(player.player_id, onHold);
  if (!canPeek) {
    return (
      <div
        className={`opponent-seat ${current ? 'is-current' : ''} ${!active ? 'is-out' : ''}`}
        aria-label={label}
      >
        {content}
      </div>
    );
  }
  return (
    <button
      type="button"
      className={`opponent-seat peekable ${current ? 'is-current' : ''}`}
      aria-label={label}
      {...holdHandlers}
    >
      {content}
    </button>
  );
}

function BidForm({
  disabled,
  statusLabel,
  category,
  setCategory,
  primaryRank,
  setPrimaryRank,
  secondaryRank,
  setSecondaryRank,
  suit,
  setSuit,
  onSubmit,
}: {
  disabled: boolean;
  statusLabel: string;
  category: BidCategory;
  setCategory: (value: BidCategory) => void;
  primaryRank: string;
  setPrimaryRank: (value: string) => void;
  secondaryRank: string;
  setSecondaryRank: (value: string) => void;
  suit: string;
  setSuit: (value: string) => void;
  onSubmit: () => void;
}) {
  const selected = categories.find((item) => item.value === category)!;
  const secondaryRanks = ranks.filter((rank) =>
    category === 'two_pairs'
      ? ranks.indexOf(rank) < ranks.indexOf(primaryRank)
      : rank !== primaryRank,
  );
  useEffect(() => {
    if (category === 'two_pairs' && primaryRank === '9') setPrimaryRank('10');
    if (!secondaryRanks.includes(secondaryRank))
      setSecondaryRank(secondaryRanks[0] ?? '9');
  }, [category, primaryRank]);
  return (
    <form
      className="bid-form"
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit();
      }}
    >
      <label htmlFor="bid-category">Combination</label>
      <select
        id="bid-category"
        value={category}
        disabled={disabled}
        onChange={(event) => setCategory(event.target.value as BidCategory)}
      >
        {categories.map((item) => (
          <option key={item.value} value={item.value}>
            {item.label}
          </option>
        ))}
      </select>
      {selected.fields.includes('rank') && (
        <RankSelect
          id="bid-rank"
          label="Rank"
          value={primaryRank}
          disabled={disabled}
          onChange={setPrimaryRank}
        />
      )}
      {selected.fields.includes('two-ranks') && (
        <div className="rank-pair-fields">
          <RankSelect
            id="bid-primary-rank"
            label={category === 'two_pairs' ? 'Higher pair' : 'Three of a kind'}
            value={primaryRank}
            disabled={disabled}
            onChange={setPrimaryRank}
            options={category === 'two_pairs' ? ranks.slice(1) : ranks}
          />
          <RankSelect
            id="bid-secondary-rank"
            label={category === 'two_pairs' ? 'Lower pair' : 'Pair'}
            value={secondaryRank}
            disabled={disabled}
            onChange={setSecondaryRank}
            options={secondaryRanks}
          />
        </div>
      )}
      {selected.fields.includes('suit') && (
        <>
          <label htmlFor="bid-suit">Suit</label>
          <select
            id="bid-suit"
            value={suit}
            disabled={disabled}
            onChange={(event) => setSuit(event.target.value)}
          >
            {suits.map(([value, label, symbol]) => (
              <option key={value} value={value}>
                {symbol} {label}
              </option>
            ))}
          </select>
        </>
      )}
      <button className="button primary full" type="submit" disabled={disabled}>
        {statusLabel} <span aria-hidden="true">↗</span>
      </button>
    </form>
  );
}

function RankSelect({
  id,
  label,
  value,
  disabled,
  onChange,
  options = ranks,
}: {
  id: string;
  label: string;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
  options?: string[];
}) {
  return (
    <div>
      <label htmlFor={id}>{label}</label>
      <select
        id={id}
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
      >
        {options.map((rank) => (
          <option key={rank} value={rank}>
            {rank}
          </option>
        ))}
      </select>
    </div>
  );
}

function PeekButton({
  id,
  label,
  onHold,
  className = 'peek-all-button',
}: {
  id: string;
  label: string;
  onHold: (id: string, held: boolean) => void;
  className?: string;
}) {
  const holdHandlers = usePeekHold(id, onHold);
  return (
    <button
      type="button"
      className={className}
      aria-label={label}
      {...holdHandlers}
    >
      Hold to peek at all hands
    </button>
  );
}

function usePeekHold(id: string, onHold: (id: string, held: boolean) => void) {
  const release = () => onHold(id, false);
  return {
    onPointerDown: (event: ReactPointerEvent<HTMLButtonElement>) => {
      if (event.button !== 0) return;
      onHold(id, true);
      if (typeof event.currentTarget.setPointerCapture === 'function') {
        try {
          event.currentTarget.setPointerCapture(event.pointerId);
        } catch {
          // Some browsers do not allow capture for every pointer type.
        }
      }
    },
    onPointerUp: release,
    onPointerCancel: release,
    onKeyDown: (event: ReactKeyboardEvent<HTMLButtonElement>) => {
      if ((event.key === ' ' || event.key === 'Enter') && !event.repeat) {
        event.preventDefault();
        onHold(id, true);
      }
    },
    onKeyUp: (event: ReactKeyboardEvent<HTMLButtonElement>) => {
      if (event.key === ' ' || event.key === 'Enter') release();
    },
    onBlur: release,
  };
}

function CardFace({
  card,
  index = 0,
  count = 1,
  small = false,
}: {
  card: Card;
  index?: number;
  count?: number;
  small?: boolean;
}) {
  const suit = suits.find(([value]) => value === card.suit);
  const red = card.suit === 'hearts' || card.suit === 'diamonds';
  return (
    <span
      className={`playing-card ${small ? 'small-card' : ''} ${red ? 'red-suit' : ''}`}
      style={fanStyle(index, count, small ? 24 : 34, small ? 7 : 6)}
      aria-label={`${card.rank} of ${suit?.[1] ?? card.suit}`}
      role="img"
    >
      <div className="card-face-symbol regular-symbol">
        <span>{card.rank}</span>
        <strong aria-hidden="true">{suit?.[2] ?? '?'}</strong>
      </div>
      {!small && (
        <div className="card-face-symbol flipped-symbol">
          <span>{card.rank}</span>
          <strong aria-hidden="true">{suit?.[2] ?? '?'}</strong>
        </div>
      )}
    </span>
  );
}

function fanStyle(
  index: number,
  count: number,
  maxSpread: number,
  maxAngle: number,
) {
  const centered = index - (count - 1) / 2;
  const spread = Math.min(maxSpread, count > 1 ? 250 / (count - 1) : 0);
  return {
    '--fan-offset': `${centered * spread}px`,
    '--fan-rotation': `${centered * maxAngle}deg`,
  } as CSSProperties;
}

function BidCard({
  placed,
  playerName,
}: {
  placed: PlacedBid;
  playerName: (id: string | null | undefined) => string;
}) {
  return (
    <div className="center-claim">
      <span className="center-claim-label">CURRENT CLAIM</span>
      <strong>{describeBid(placed.bid)}</strong>
      <span>by {playerName(placed.player_id)}</span>
    </div>
  );
}

function RoundResultSummary({
  result,
  playerName,
}: {
  result: RoundResult;
  playerName: (id: string | null | undefined) => string;
}) {
  return (
    <div className="center-result">
      <span
        className={`result-verdict ${result.claim_satisfied ? 'true' : 'false'}`}
      >
        {result.claim_satisfied ? 'TRUE' : 'FALSE'}
      </span>
      <h1>The claim was {result.claim_satisfied ? 'true' : 'false'}</h1>
      <p>
        {playerName(result.challenged_bid.player_id)} claimed{' '}
        {describeBid(result.challenged_bid.bid)}.{' '}
        {playerName(result.challenger_id)} challenged.
      </p>
      <strong>{playerName(result.loser_id)} lost the round</strong>
    </div>
  );
}

function SettingsDialog({
  dialogRef,
  open,
  onClose,
  state,
  connected,
  connectionStatus,
  invitation,
  copied,
  copyError,
  onCopy,
  onLeave,
  result,
  playerName,
}: {
  dialogRef: RefObject<HTMLDialogElement | null>;
  open: boolean;
  onClose: () => void;
  state: RoomState;
  connected: boolean;
  connectionStatus: ConnectionStatus;
  invitation: string;
  copied: boolean;
  copyError: boolean;
  onCopy: () => void;
  onLeave?: () => void;
  result: RoundResult | null;
  playerName: (id: string | null | undefined) => string;
}) {
  const [showLink, setShowLink] = useState(false);
  return (
    <dialog
      className="settings-dialog"
      ref={dialogRef}
      aria-labelledby="settings-title"
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      onClose={onClose}
    >
      <div className="dialog-heading">
        <div>
          <span className="eyebrow">TABLE OPTIONS</span>
          <h2 id="settings-title">Settings</h2>
        </div>
        <button
          type="button"
          className="dialog-close"
          aria-label="Close settings"
          onClick={onClose}
        >
          ×
        </button>
      </div>
      <div className="settings-status">
        <span className={`connection-dot ${connected ? 'online' : ''}`} />
        {connectionStatus === 'connected'
          ? 'Connected to the table'
          : connectionStatus === 'offline'
            ? 'Offline; actions are paused'
            : 'Reconnecting to the table'}
      </div>
      <section className="settings-section">
        <h3>Room invitation</h3>
        <p className="settings-code">{state.room_code}</p>
        <button className="settings-option" type="button" onClick={onCopy}>
          {copied ? 'Invite link copied' : 'Copy invite link'}{' '}
          <span aria-hidden="true">↗</span>
        </button>
        {copyError && (
          <button
            className="settings-option"
            type="button"
            onClick={() => setShowLink((value) => !value)}
          >
            {showLink ? 'Hide invite link' : 'Show invite link'}
          </button>
        )}
        {showLink && (
          <input
            className="settings-link"
            readOnly
            value={invitation}
            onFocus={(event) => event.target.select()}
          />
        )}
      </section>
      <section className="settings-section">
        <h3>How bids rank</h3>
        <p className="settings-copy">
          High card → pair → two pairs → low straight (9–K) → high straight
          (10–A) → three of a kind → full house → flush → four of a kind →
          straight flush (9–K) → royal straight flush (10–A). Suits never break
          ties. A flush means any five cards of the named suit.
        </p>
        <p className="settings-copy">
          Claims are checked across all cards dealt this round. Bluffing is
          allowed.
        </p>
      </section>
      {result && (
        <details className="settings-section history-details">
          <summary>Round {result.round_id} reveal</summary>
          <div className="settings-history">
            <p>
              {result.claim_satisfied
                ? 'The claim was true.'
                : 'The claim was false.'}{' '}
              {playerName(result.loser_id)} lost.
            </p>
            {result.revealed_hands.map((hand) => (
              <div className="settings-revealed-hand" key={hand.player_id}>
                <span>{playerName(hand.player_id)}</span>
                {hand.cards.map((card, index) => (
                  <span
                    key={`${hand.player_id}-${card.rank}-${card.suit}-${index}`}
                  >
                    {card.rank}
                    {suits.find(([value]) => value === card.suit)?.[2]}
                  </span>
                ))}
              </div>
            ))}
          </div>
        </details>
      )}
      {onLeave && (
        <button className="settings-leave" type="button" onClick={onLeave}>
          Leave room
        </button>
      )}
    </dialog>
  );
}

function describeBid(bid: Bid): string {
  const label = categoryLabels.get(bid.category) ?? bid.category;
  if (
    bid.category === 'high_card' ||
    bid.category === 'pair' ||
    bid.category === 'three_of_a_kind' ||
    bid.category === 'four_of_a_kind'
  )
    return `${label}: ${bid.primary_rank}`;
  if (bid.category === 'two_pairs')
    return `Two pairs: ${bid.primary_rank}s and ${bid.secondary_rank}s`;
  if (bid.category === 'full_house')
    return `Full house: ${bid.primary_rank}s over ${bid.secondary_rank}s`;
  if (bid.suit)
    return `${label} of ${suits.find(([value]) => value === bid.suit)?.[1] ?? bid.suit}`;
  return label;
}
