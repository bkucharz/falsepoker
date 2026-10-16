import { useEffect, useState, useSyncExternalStore } from 'react';
import { leaveRoom } from './api';
import { RoomConnection } from './connection';
import type { Membership } from './protocol';
import { useMutation, MutationError } from './mutation';
import { Welcome } from './Welcome';
import { Lobby } from './Lobby';

export function RoomScreen({
  code,
  expectedID,
  onJoined,
  onHome,
  onNewSession,
}: {
  code: string;
  expectedID?: string;
  onJoined: (m: Membership) => void;
  onHome: () => void;
  onNewSession: () => void;
}) {
  const [connection] = useState(() => new RoomConnection(code, expectedID));
  const view = useSyncExternalStore(
    connection.subscribe,
    connection.getSnapshot,
  );
  const task = useMutation();
  useEffect(() => {
    connection.start();
    return () => connection.stop();
  }, [connection]);
  const leave = () => {
    if (view.state)
      task.run(leaveRoom(code, view.state.room_id), () => {
        connection.stop();
        onHome();
      });
  };
  if (view.status === 'unavailable')
    return (
      <>
        <div className="recovery">
          <MutationError task={task} onNewSession={onNewSession} />
          <div className="notice" role="status">
            {view.error}
          </div>
          {view.errorCode === 'AUTH_REQUIRED' ||
          view.errorCode === 'SESSION_EXPIRED' ? (
            <button className="button primary" onClick={onNewSession}>
              Start a new session
            </button>
          ) : null}
          <button className="button ghost" onClick={onHome}>
            Back to home
          </button>
        </div>
        {!task.busy &&
        !task.uncertain &&
        (view.errorCode === 'ROOM_UNAVAILABLE' ||
          view.errorCode === 'STALE_ROOM') ? (
          <Welcome
            invitation={code}
            onJoined={onJoined}
            onNewSession={onNewSession}
          />
        ) : null}
      </>
    );
  if (!view.state)
    return (
      <main className="loading-view" role="status">
        <span className="loading-spade" aria-hidden="true">
          ♠
        </span>
        <h1>
          {view.status === 'reconnecting'
            ? 'Reconnecting…'
            : 'Opening your room…'}
        </h1>
        <p>Restoring your seat from the server.</p>
        <button className="button ghost" onClick={onHome}>
          Back to home
        </button>
      </main>
    );
  return (
    <>
      <div className="mutation-notice">
        <MutationError task={task} onNewSession={onNewSession} />
      </div>
      <Lobby
        view={{ ...view, pending: view.pending || task.busy || task.uncertain }}
        command={connection.command.bind(connection)}
        onLeave={leave}
      />
    </>
  );
}
