import { useEffect, useState } from 'react';
import { initializeSession } from './api';
import { pathCode, roomPath, type Membership, type Session } from './protocol';
import { RoomScreen } from './RoomScreen';
import { Welcome } from './Welcome';

function Brand() {
  return (
    <a className="brand" href="/" aria-label="False Poker home">
      <span className="brand-mark" aria-hidden="true">
        ♠
      </span>
      <span>
        FALSE<span className="brand-light">POKER</span>
      </span>
    </a>
  );
}

export function App() {
  const [path, setPath] = useState(location.pathname);
  const [session, setSession] = useState<Session | null>(null);
  const [sessionError, setSessionError] = useState<string | null>(null);
  const [boot, setBoot] = useState(0);
  const [membership, setMembership] = useState<Membership | null>(null);
  useEffect(() => {
    let alive = true;
    setSessionError(null);
    void initializeSession(boot > 0)
      .then((value) => {
        if (alive) setSession(value);
      })
      .catch((error) => {
        if (alive) setSessionError(error.message);
      });
    return () => {
      alive = false;
    };
  }, [boot]);
  useEffect(() => {
    const pop = () => setPath(location.pathname);
    window.addEventListener('popstate', pop);
    return () => window.removeEventListener('popstate', pop);
  }, []);
  const navigate = (next: string) => {
    history.pushState(null, '', next);
    setPath(next);
  };
  const joined = (value: Membership) => {
    setMembership(value);
    navigate(roomPath(value.room_code));
  };
  const code = pathCode(path);
  return (
    <>
      <header className="site-header">
        <Brand />
        <span className="header-note">
          A game between friends <span aria-hidden="true">✦</span>
        </span>
      </header>
      {!session ? (
        <main className="loading-view">
          <h1>{sessionError ? 'Let’s reconnect.' : 'Pull up a chair.'}</h1>
          <p role={sessionError ? 'alert' : 'status'}>
            {sessionError ?? 'Preparing your private session…'}
          </p>
          {sessionError && (
            <button
              className="button primary"
              onClick={() => setBoot((x) => x + 1)}
            >
              Try session again
            </button>
          )}
        </main>
      ) : code ? (
        <RoomScreen
          key={`${code}:${membership?.room_id ?? ''}:${boot}`}
          code={code}
          expectedID={
            membership?.room_code === code ? membership.room_id : undefined
          }
          onJoined={joined}
          onHome={() => {
            setMembership(null);
            navigate('/');
          }}
          onNewSession={() => {
            setSession(null);
            setMembership(null);
            setBoot((x) => x + 1);
          }}
        />
      ) : path === '/' ? (
        <Welcome
          onJoined={joined}
          onNewSession={() => {
            setSession(null);
            setMembership(null);
            setBoot((x) => x + 1);
          }}
        />
      ) : (
        <main className="loading-view">
          <h1>That invitation doesn’t look right.</h1>
          <p>Room codes contain 12 hexadecimal characters.</p>
          <button className="button primary" onClick={() => navigate('/')}>
            Back to home
          </button>
        </main>
      )}
      <footer className="site-footer">
        <span>FALSE POKER</span>
        <p>Read your friends. Trust your gut.</p>
        <span aria-hidden="true">♣ ♦ ♠ ♥</span>
      </footer>
    </>
  );
}
