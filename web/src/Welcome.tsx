import { useState, type FormEvent } from 'react';
import { createRoom, joinRoom } from './api';
import {
  nameError,
  normalizeCode,
  validCode,
  type Membership,
} from './protocol';
import { useMutation, MutationError } from './mutation';

export function Welcome({
  invitation = '',
  onJoined,
  onNewSession,
}: {
  invitation?: string;
  onNewSession?: () => void;
  onJoined: (membership: Membership) => void;
}) {
  const [mode, setMode] = useState<'create' | 'join'>(
    invitation ? 'join' : 'create',
  );
  const [name, setName] = useState('');
  const [code, setCode] = useState(invitation);
  const [validation, setValidation] = useState<string | null>(null);
  const task = useMutation();
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (task.busy || task.uncertain) return;
    const error =
      nameError(name) ??
      (mode === 'join' && !validCode(code)
        ? 'Enter the 12-character room code from your invitation.'
        : null);
    setValidation(error);
    if (error) return;
    task.run(
      mode === 'create'
        ? createRoom(name)
        : joinRoom(normalizeCode(code), name),
      onJoined,
    );
  };
  return (
    <main className="welcome">
      <section className="hero" aria-labelledby="hero-title">
        <p className="eyebrow">
          <span className="tiny-suit" aria-hidden="true">
            ♦
          </span>{' '}
          A LITTLE POKER. A LOT OF BLUFFING.
        </p>
        <h1 id="hero-title">
          Good cards.
          <br />
          Better <em>bluffs.</em>
        </h1>
        <p className="hero-copy">
          Bring your friends. Read the room. Make a claim they won’t believe.
        </p>
        <div className="card-art" aria-hidden="true">
          <div className="art-card back">♠</div>
          <div className="art-card ace">
            <span>
              A<small>♠</small>
            </span>
            <b>♠</b>
          </div>
          <div className="art-chip">?</div>
        </div>
        <div className="hero-facts">
          <span>2–24 players</span>
          <span>No accounts</span>
          <span>Just friends</span>
        </div>
      </section>
      <section className="entry panel" aria-labelledby="entry-title">
        <p className="eyebrow">YOUR NEXT GOOD GAME</p>
        <h2 id="entry-title">Take a seat.</h2>
        <p className="muted">
          A private room, a familiar name, and a little nerve.
        </p>
        <div className="mode-switch" aria-label="Room action">
          <button
            type="button"
            aria-pressed={mode === 'create'}
            disabled={task.busy || task.uncertain}
            onClick={() => {
              setMode('create');
              setValidation(null);
            }}
          >
            Create a room
          </button>
          <button
            type="button"
            aria-pressed={mode === 'join'}
            disabled={task.busy || task.uncertain}
            onClick={() => {
              setMode('join');
              setValidation(null);
            }}
          >
            Join a room
          </button>
        </div>
        <form onSubmit={submit} noValidate>
          <label htmlFor="display-name">Your display name</label>
          <input
            id="display-name"
            autoComplete="nickname"
            placeholder="What should we call you?"
            value={name}
            disabled={task.busy || task.uncertain}
            aria-describedby="name-hint form-error"
            onChange={(e) => setName(e.target.value)}
          />
          <p id="name-hint" className="hint">
            1–32 characters. This name is just for this room.
          </p>
          {mode === 'join' && (
            <>
              <label htmlFor="room-code">Room code</label>
              <input
                id="room-code"
                autoCapitalize="none"
                spellCheck={false}
                autoComplete="off"
                placeholder="e.g. a1b2c3d4e5f6"
                value={code}
                disabled={task.busy || task.uncertain}
                onChange={(e) => setCode(e.target.value)}
              />
            </>
          )}
          {validation && (
            <p id="form-error" className="field-error" role="alert">
              {validation}
            </p>
          )}
          <button
            className="button primary full"
            disabled={task.busy || task.uncertain}
            type="submit"
          >
            {task.busy
              ? 'One moment…'
              : mode === 'create'
                ? 'Create private room'
                : 'Join room'}
            <span aria-hidden="true">↗</span>
          </button>
        </form>
        <MutationError task={task} onNewSession={onNewSession} />
        <p className="entry-note">
          <span aria-hidden="true">♧</span> No registration. Share a code and
          you’re in.
        </p>
      </section>
      <section className="how-it-works" aria-label="How to play">
        <div>
          <span>01</span>
          <h3>Make a claim</h3>
          <p>Bid on a combination across everyone’s cards.</p>
        </div>
        <div>
          <span>02</span>
          <h3>Raise or call it</h3>
          <p>Believe the bluff? Raise. Don’t? Challenge.</p>
        </div>
        <div>
          <span>03</span>
          <h3>Stay in the game</h3>
          <p>Lose a round, gain a card. Last player wins.</p>
        </div>
      </section>
    </main>
  );
}
