import { useEffect, useRef, useState } from 'react';
import { ApiError, type Mutation } from './api';

export function useMutation() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [errorCode, setErrorCode] = useState<string | null>(null);
  const [uncertain, setUncertain] = useState(false);
  const operation = useRef<(() => Promise<void>) | null>(null);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const execute = async () => {
    if (!operation.current) return;
    setBusy(true);
    setError(null);
    setErrorCode(null);
    try {
      await operation.current();
      if (mounted.current) {
        operation.current = null;
        setUncertain(false);
      }
    } catch (error) {
      if (mounted.current) {
        setErrorCode(error instanceof ApiError ? error.code : null);
        setError(
          error instanceof Error
            ? error.message
            : 'Something went wrong. Please try again.',
        );
        const retry =
          error instanceof ApiError &&
          (error.retryable || error.code === 'UNCERTAIN');
        setUncertain(retry);
        if (!retry) operation.current = null;
      }
    } finally {
      if (mounted.current) setBusy(false);
    }
  };
  const run = <T,>(mutation: Mutation<T>, success: (value: T) => void) => {
    if (operation.current) return;
    operation.current = async () => {
      const value = await mutation.run();
      if (mounted.current) success(value);
    };
    void execute();
  };
  return { busy, error, errorCode, uncertain, run, retry: execute };
}
export function MutationError({
  task,
  onNewSession,
}: {
  task: ReturnType<typeof useMutation>;
  onNewSession?: () => void;
}) {
  if (!task.error) return null;
  return (
    <div className="notice error" role="alert">
      <p>{task.error}</p>
      {onNewSession &&
        (task.errorCode === 'SESSION_EXPIRED' ||
          task.errorCode === 'AUTH_REQUIRED') && (
          <button className="button small" onClick={onNewSession}>
            Start a new session
          </button>
        )}
      {task.uncertain && (
        <button
          className="button small"
          disabled={task.busy}
          onClick={() => void task.retry()}
        >
          Retry original request
        </button>
      )}
    </div>
  );
}
