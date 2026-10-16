import {
  requestID,
  type Membership,
  type RoomState,
  type Session,
} from './protocol';

export class ApiError extends Error {
  constructor(
    public code: string,
    message: string,
    public retryable = false,
  ) {
    super(message);
  }
}
export const terminalCodes = new Set([
  'AUTH_REQUIRED',
  'SESSION_EXPIRED',
  'ROOM_UNAVAILABLE',
  'STALE_ROOM',
  'ORIGIN_NOT_ALLOWED',
  'UNSUPPORTED_PROTOCOL',
]);
export async function request<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`/api/v1${path}`, {
      ...init,
      credentials: 'same-origin',
      signal: init.signal ?? AbortSignal.timeout(10_000),
      headers: { 'Content-Type': 'application/json', ...init.headers },
    });
  } catch {
    throw new ApiError(
      'NETWORK',
      'Connection interrupted. Please try again.',
      true,
    );
  }
  let body;
  try {
    body = await response.json();
  } catch {
    throw new ApiError(
      'RESPONSE',
      'The server returned an unreadable response.',
      true,
    );
  }
  if (!response.ok)
    throw new ApiError(
      body.error?.code ?? 'RESPONSE',
      body.error?.message ?? 'Request failed.',
      body.error?.retryable ?? false,
    );
  return body as T;
}
let sessionFlight: Promise<Session> | undefined;
// Do not cancel this shared bootstrap on React effect cleanup: another mount/tab
// may be waiting, and the cookie must arrive before releasing the origin lock.
export function initializeSession(replaceExpired = false): Promise<Session> {
  if (!sessionFlight) {
    sessionFlight = (async () => {
      if (!navigator.locks)
        throw new ApiError(
          'BROWSER',
          'Use a current browser on localhost or HTTPS to play.',
        );
      return navigator.locks.request('falsepoker-session', async () => {
        // A non-secret marker prevents the next tab from silently creating a
        // replacement after an expired-cookie response cleared the shared cookie.
        if (replaceExpired)
          localStorage.removeItem('falsepoker-session-expired');
        if (localStorage.getItem('falsepoker-session-expired')) {
          throw new ApiError(
            'SESSION_EXPIRED',
            'Your session expired. Start a new session explicitly to continue.',
          );
        }
        try {
          return await request<Session>('/session', {
            method: 'POST',
            body: '{}',
          });
        } catch (error) {
          if (error instanceof ApiError && error.code === 'SESSION_EXPIRED')
            localStorage.setItem('falsepoker-session-expired', 'true');
          throw error;
        }
      });
    })().finally(() => {
      sessionFlight = undefined;
    });
  }
  return sessionFlight;
}
export const roomInfo = (code: string, signal?: AbortSignal) =>
  request<{ state: RoomState }>(`/rooms/${code}`, { signal });

// A mutation object represents one intent. Explicit retries retain its key/body;
// an uncertain response must never become a new create/join/leave operation.
export class Mutation<T> {
  private key = requestID();
  private started = performance.now();
  private flight?: Promise<T>;
  private payload: string;
  constructor(
    private path: string,
    body: object,
  ) {
    this.payload = JSON.stringify(body);
  }
  run(): Promise<T> {
    if (performance.now() - this.started >= 240_000) {
      return Promise.reject(
        new ApiError(
          'UNCERTAIN',
          'The retry window ended. Review the room before taking another action.',
        ),
      );
    }
    if (!this.flight)
      this.flight = request<T>(this.path, {
        method: 'POST',
        body: this.payload,
        headers: { 'Idempotency-Key': this.key },
      }).finally(() => {
        this.flight = undefined;
      });
    return this.flight;
  }
}
export const createRoom = (name: string) =>
  new Mutation<Membership>('/rooms', { display_name: name.trim() });
export const joinRoom = (code: string, name: string) =>
  new Mutation<Membership>(`/rooms/${code}/join`, {
    display_name: name.trim(),
  });
export const leaveRoom = (code: string, roomID: string) =>
  new Mutation<{ left: boolean }>(`/rooms/${code}/leave`, { room_id: roomID });
