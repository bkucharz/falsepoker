import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, Mutation, initializeSession } from './api';
const response = (body: object, ok = true) =>
  ({ ok, json: async () => body }) as Response;
afterEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
describe('anonymous ownership and HTTP intent', () => {
  it('shares in-flight bootstrap and waits for the origin lock', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(response({ player_id: 'alice', protocol_version: 1 }));
    const lock = vi.fn(async (_: string, callback: () => Promise<unknown>) =>
      callback(),
    );
    vi.stubGlobal('fetch', fetch);
    vi.stubGlobal('navigator', { locks: { request: lock } });
    const first = initializeSession(),
      second = initializeSession();
    expect(first).toBe(second);
    expect((await first).player_id).toBe('alice');
    expect(lock).toHaveBeenCalledOnce();
    expect(fetch).toHaveBeenCalledOnce();
    expect(fetch.mock.calls[0][1].credentials).toBe('same-origin');
  });
  it('surfaces expired ownership instead of silently replacing it', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        response(
          { error: { code: 'SESSION_EXPIRED', message: 'Expired' } },
          false,
        ),
      );
    vi.stubGlobal('fetch', fetch);
    vi.stubGlobal('navigator', {
      locks: {
        request: async (_: string, callback: () => Promise<unknown>) =>
          callback(),
      },
    });
    await expect(initializeSession()).rejects.toMatchObject({
      code: 'SESSION_EXPIRED',
    });
    await expect(initializeSession()).rejects.toMatchObject({
      code: 'SESSION_EXPIRED',
    });
    expect(fetch).toHaveBeenCalledOnce();
    fetch.mockResolvedValue(
      response({ player_id: 'new-player', protocol_version: 1 }),
    );
    expect((await initializeSession(true)).player_id).toBe('new-player');
  });
  it('reuses the HTTP key and body after an uncertain response and deduplicates clicks', async () => {
    const fetch = vi
      .fn()
      .mockRejectedValueOnce(new TypeError('Offline'))
      .mockResolvedValue(response({ left: true }));
    vi.stubGlobal('fetch', fetch);
    const mutation = new Mutation('/rooms/code/leave', { room_id: 'r_one' });
    const first = mutation.run();
    expect(mutation.run()).toBe(first);
    await expect(first).rejects.toBeInstanceOf(ApiError);
    await mutation.run();
    expect(fetch.mock.calls[0][1].body).toBe(fetch.mock.calls[1][1].body);
    expect(fetch.mock.calls[0][1].headers['Idempotency-Key']).toBe(
      fetch.mock.calls[1][1].headers['Idempotency-Key'],
    );
    expect(fetch.mock.calls[0][1].headers['Idempotency-Key']).toMatch(
      /^[a-f0-9]{32}$/,
    );
  });
  it('captures the original HTTP body rather than rereading mutable form data', async () => {
    const fetch = vi
      .fn()
      .mockRejectedValueOnce(new TypeError('Offline'))
      .mockResolvedValue(response({ left: true }));
    vi.stubGlobal('fetch', fetch);
    const body = { room_id: 'r_original' };
    const mutation = new Mutation('/rooms/code/leave', body);
    await expect(mutation.run()).rejects.toBeInstanceOf(ApiError);
    body.room_id = 'r_different';
    await mutation.run();
    expect(fetch.mock.calls[1][1].body).toBe(
      JSON.stringify({ room_id: 'r_original' }),
    );
  });
  it('does not send uncertain HTTP intent after its bounded retry window', async () => {
    vi.useFakeTimers();
    const fetch = vi.fn().mockRejectedValue(new TypeError('Offline'));
    vi.stubGlobal('fetch', fetch);
    const mutation = new Mutation('/rooms', { display_name: 'Alice' });
    await expect(mutation.run()).rejects.toBeInstanceOf(ApiError);
    vi.advanceTimersByTime(240_000);
    await expect(mutation.run()).rejects.toMatchObject({ code: 'UNCERTAIN' });
    expect(fetch).toHaveBeenCalledOnce();
  });
});
