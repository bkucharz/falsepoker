import { useEffect, useRef, useState } from 'react';
import type { RoomConnection, ConnectionView } from './connection';
import { roomPath } from './protocol';
import { Gameplay } from './Gameplay';

export function Lobby({
  view,
  command,
  onLeave,
}: {
  view: ConnectionView;
  command: RoomConnection['command'];
  onLeave: () => void;
}) {
  const state = view.state!;
  const ready = state.members.filter((member) => member.ready);
  const signature = ready.map((member) => member.player_id).join(',');
  const [shuffle, setShuffle] = useState(true);
  const [order, setOrder] = useState(ready.map((member) => member.player_id));
  const [reviewed, setReviewed] = useState(signature);
  const [firstBidder, setFirstBidder] = useState('');
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [gameplayDismissed, setGameplayDismissed] = useState(false);
  const myself = state.members.find(
    (member) => member.player_id === state.recipient_id,
  )!;
  const admin = state.admin_id === state.recipient_id;
  const playing = state.game?.phase === 'playing';
  const finished = state.game?.phase === 'finished';
  const inGameView = (playing || finished) && !gameplayDismissed;
  const disabled = view.status !== 'connected' || view.pending;
  useEffect(() => {
    if (playing) setGameplayDismissed(false);
  }, [playing, state.game_id]);
  const invalidRoster =
    (!shuffle && reviewed !== signature) ||
    (!!firstBidder && !ready.some((m) => m.player_id === firstBidder));
  // Display current members safely while preserving the fact that an explicit
  // order needs review after roster changes. Random choices always use the server.
  const displayedOrder = [
    ...order.filter((id) => ready.some((m) => m.player_id === id)),
    ...ready.map((m) => m.player_id).filter((id) => !order.includes(id)),
  ];
  const move = (index: number, delta: number) => {
    const next = [...displayedOrder];
    [next[index], next[index + delta]] = [next[index + delta], next[index]];
    setOrder(next);
    setReviewed(signature);
  };
  const invitation = `${location.origin}${roomPath(state.room_code)}`;
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(invitation);
      setCopied(true);
      setCopyError(false);
    } catch {
      setCopyError(true);
    }
  };
  const leave = () => {
    if (playing && state.role === 'participant') setConfirm(true);
    else onLeave();
  };
  return (
    <main className={`room-layout ${inGameView ? 'is-playing' : ''}`}>
      <section className="room-heading">
        <div>
          <p className="eyebrow">YOUR PRIVATE ROOM</p>
          <h1>
            {playing
              ? 'The game is on.'
              : state.game
                ? 'One more game?'
                : 'The gang’s gathering.'}
          </h1>
          <p className="muted">
            {playing
              ? 'Seats are set. Late arrivals join as spectators.'
              : 'Get comfortable. Ready up when you’re in.'}
          </p>
        </div>
        <div className="room-actions">
          <p
            className={`connection-badge ${view.status === 'offline' ? 'is-offline' : ''}`}
            role="status"
          >
            Your connection:{' '}
            {view.status === 'connected'
              ? 'Connected'
              : view.status === 'offline'
                ? 'Offline'
                : 'Reconnecting'}
          </p>
          <button
            className="button ghost"
            onClick={leave}
            disabled={view.pending}
          >
            Leave room
          </button>
        </div>
      </section>
      <section className="invite-bar" aria-label="Invite friends">
        <div>
          <span className="eyebrow">ROOM CODE</span>
          <strong className="room-code">{state.room_code}</strong>
        </div>
        <button className="button light" onClick={() => void copy()}>
          {copied ? 'Link copied ✓' : 'Copy invite link'}
          <span aria-hidden="true">↗</span>
        </button>
        {copyError && (
          <label className="copy-fallback">
            Copy this invitation
            <input
              readOnly
              value={invitation}
              onFocus={(event) => event.target.select()}
            />
          </label>
        )}
      </section>
      {admin && !playing && (
        <a className="button light mobile-setup-shortcut" href="#setup-title">
          Jump to game setup ↓
        </a>
      )}
      {view.status !== 'connected' && (
        <div className="notice" role="status">
          {view.status === 'offline'
            ? 'You’re offline. Actions are paused until your connection returns.'
            : 'Reconnecting to your room. Actions will return after a fresh update.'}
        </div>
      )}
      {view.error && !playing && (
        <div className="notice error" role="alert">
          {view.error}
          {view.errorCode?.startsWith('STALE_') && (
            <p>The room changed. Review your choices before trying again.</p>
          )}
        </div>
      )}
      {state.game && inGameView && (
        <Gameplay
          state={state}
          connected={view.status === 'connected'}
          connectionStatus={view.status}
          pending={view.pending}
          error={view.error}
          errorCode={view.errorCode}
          command={command}
          onLeave={leave}
          onExitGame={() => setGameplayDismissed(true)}
        />
      )}
      <div className={`lobby-columns ${playing ? 'game-in-progress' : ''}`}>
        <section className="players-section" aria-labelledby="players-title">
          <div className="section-heading">
            <h2 id="players-title">
              Around the room{' '}
              <span className="count">{state.members.length}</span>
            </h2>
            <span className="muted">
              {playing
                ? `${state.game!.players.filter((p) => !p.eliminated && !p.withdrawn).length} playing`
                : `${ready.length} ready`}
            </span>
          </div>
          <ul className="player-grid">
            {state.members.map((member, index) => (
              <li
                className={`player-card ${member.player_id === state.recipient_id ? 'is-you' : ''}`}
                key={member.player_id}
              >
                <span className={`avatar tone-${index % 4}`} aria-hidden="true">
                  {Array.from(member.display_name)[0]?.toUpperCase()}
                </span>
                <div className="player-details">
                  <strong>
                    {member.display_name}
                    {member.player_id === state.recipient_id && (
                      <span className="you-label"> you</span>
                    )}
                  </strong>
                  <span className="player-role">
                    {member.player_id === state.admin_id
                      ? 'Room admin'
                      : 'Room member'}
                  </span>
                </div>
                <span className={`ready-badge ${member.ready ? 'yes' : ''}`}>
                  {playing
                    ? state.game!.players.some(
                        (p) =>
                          p.player_id === member.player_id &&
                          !p.eliminated &&
                          !p.withdrawn,
                      )
                      ? 'Playing'
                      : 'Watching'
                    : member.ready
                      ? '✓ Ready'
                      : 'Not ready'}
                </span>
              </li>
            ))}
          </ul>
          {!playing && (
            <div className="readiness">
              <div>
                <strong>
                  {myself.ready ? 'You’re in.' : 'Playing this round?'}
                </strong>
                <p className="hint">
                  {myself.ready
                    ? 'You’ll join when the admin starts.'
                    : 'Ready players join the game. Others can watch.'}
                </p>
              </div>
              <button
                className={`button ${myself.ready ? 'light' : 'primary'}`}
                disabled={disabled}
                onClick={() => command('ready', { ready: !myself.ready })}
              >
                {view.pending
                  ? 'Updating…'
                  : myself.ready
                    ? 'Not ready'
                    : 'I’m ready'}
              </button>
            </div>
          )}
        </section>
        <aside className="panel setup" aria-labelledby="setup-title">
          <p className="eyebrow">
            {playing
              ? 'A LITTLE NERVE GOES A LONG WAY'
              : 'BEFORE THE FIRST BLUFF'}
          </p>
          <h2 id="setup-title" tabIndex={-1}>
            {playing
              ? 'At the table.'
              : admin
                ? 'Set the table.'
                : 'Almost game time.'}
          </h2>
          {admin && !playing ? (
            <>
              <p className="muted">
                You’re the admin. Everyone who’s ready gets a seat—even if
                you’re just watching.
              </p>
              <label htmlFor="seat-mode">Seat order</label>
              <select
                id="seat-mode"
                value={shuffle ? 'random' : 'manual'}
                disabled={disabled}
                onChange={(e) => {
                  setShuffle(e.target.value === 'random');
                  setOrder(ready.map((m) => m.player_id));
                  setReviewed(signature);
                }}
              >
                <option value="random">Shuffle randomly</option>
                <option value="manual">Choose seat order</option>
              </select>
              {!shuffle && (
                <ol className="seat-list" aria-label="Seat order">
                  {displayedOrder.map((id, index) => (
                    <li key={id}>
                      <span>
                        {index + 1}.{' '}
                        {ready.find((m) => m.player_id === id)!.display_name}
                      </span>
                      <button
                        className="icon-button"
                        disabled={disabled || index === 0}
                        aria-label={`Move ${ready.find((m) => m.player_id === id)!.display_name} up`}
                        onClick={() => move(index, -1)}
                      >
                        ↑
                      </button>
                      <button
                        className="icon-button"
                        disabled={
                          disabled || index === displayedOrder.length - 1
                        }
                        aria-label={`Move ${ready.find((m) => m.player_id === id)!.display_name} down`}
                        onClick={() => move(index, 1)}
                      >
                        ↓
                      </button>
                    </li>
                  ))}
                </ol>
              )}
              <label htmlFor="first-bidder">First bidder</label>
              <select
                id="first-bidder"
                value={firstBidder}
                disabled={disabled}
                onChange={(e) => setFirstBidder(e.target.value)}
              >
                <option value="">Choose randomly</option>
                {ready.map((m) => (
                  <option key={m.player_id} value={m.player_id}>
                    {m.display_name}
                  </option>
                ))}
                {firstBidder &&
                  !ready.some((m) => m.player_id === firstBidder) && (
                    <option value={firstBidder}>
                      Player no longer ready — choose again
                    </option>
                  )}
              </select>
              {invalidRoster && (
                <div className="notice" role="status">
                  <p>
                    The ready players changed. Review the order and first
                    bidder.
                  </p>
                  <button
                    className="button small"
                    disabled={disabled}
                    onClick={() => {
                      setOrder(displayedOrder);
                      setReviewed(signature);
                      if (!ready.some((m) => m.player_id === firstBidder))
                        setFirstBidder('');
                    }}
                  >
                    Confirm updated choices
                  </button>
                </div>
              )}
              <p className="hint">
                {ready.length < 2
                  ? 'At least 2 players need to be ready.'
                  : ready.length > 24
                    ? 'Only 24 players can participate. Some players need to become unready.'
                    : `${ready.length} ready players will participate.`}
              </p>
              <button
                className="button primary full"
                disabled={
                  disabled ||
                  invalidRoster ||
                  ready.length < 2 ||
                  ready.length > 24
                }
                onClick={() =>
                  command('start', {
                    seat_order: shuffle ? null : displayedOrder,
                    shuffle_seats: shuffle,
                    first_bidder: firstBidder || null,
                  })
                }
              >
                Start game<span aria-hidden="true">↗</span>
              </button>
            </>
          ) : (
            <>
              <p className="muted">
                {playing
                  ? 'This game is underway. New arrivals can watch; the next game starts with fresh readiness.'
                  : 'The room admin chooses the seats and first bidder, then starts the game.'}
              </p>
              <div className="mini-rule">
                <span aria-hidden="true">♠</span>
                <p>
                  Good to know: your claim is about{' '}
                  <strong>everyone’s cards</strong>, not just your own.
                </p>
              </div>
            </>
          )}
        </aside>
      </div>
      {confirm && (
        <LeaveDialog
          onCancel={() => setConfirm(false)}
          onConfirm={() => {
            setConfirm(false);
            onLeave();
          }}
        />
      )}
    </main>
  );
}
function LeaveDialog({
  onCancel,
  onConfirm,
}: {
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    dialog.current?.showModal();
    return () => {
      dialog.current?.close();
      previous?.focus();
    };
  }, []);
  return (
    <dialog
      ref={dialog}
      aria-labelledby="leave-title"
      onCancel={(event) => {
        event.preventDefault();
        onCancel();
      }}
    >
      <h2 id="leave-title">Leave this game?</h2>
      <p>
        You’ll withdraw from the game. This also leaves the room in your other
        tabs. Joining again makes you a spectator while this game continues.
      </p>
      <div className="dialog-actions">
        <button className="button light" autoFocus onClick={onCancel}>
          Stay in room
        </button>
        <button className="button danger" onClick={onConfirm}>
          Leave game
        </button>
      </div>
    </dialog>
  );
}
