import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, roomInfo } from './api';
import { RoomConnection } from './connection';
import { room } from './testing/fixtures';
import type { RoomState } from './protocol';
vi.mock('./api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./api')>()),
  roomInfo: vi.fn(),
}));
class Socket {
  static OPEN = 1;
  static instances: Socket[] = [];
  readyState = 1;
  onmessage?: (event: { data: string }) => void;
  onclose?: (event: { code: number }) => void;
  onerror?: () => void;
  sent: string[] = [];
  url: string;
  constructor(
    url: string | URL,
    public protocol: string,
  ) {
    this.url = String(url);
    Socket.instances.push(this);
  }
  send(value: string) {
    this.sent.push(value);
  }
  close() {
    this.readyState = 3;
  }
  drop(code = 1006) {
    this.readyState = 3;
    this.onclose?.({ code });
  }
  receive(message: object) {
    this.onmessage?.({ data: JSON.stringify(message) });
  }
  snapshot(state = room()) {
    this.receive({ type: 'snapshot', state });
  }
}
const flush = async () => {
  for (let i = 0; i < 6; i++) await Promise.resolve();
};
let client: RoomConnection;
let source: RoomState;
beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal('WebSocket', Socket);
  Socket.instances = [];
  source = room();
  vi.mocked(roomInfo).mockImplementation(async () => ({ state: source }));
  client = new RoomConnection(source.room_code);
});
afterEach(() => {
  client.stop();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
async function connected() {
  client.start();
  await flush();
  const socket = Socket.instances.at(-1)!;
  socket.snapshot(source);
  return socket;
}
describe('room connection', () => {
  it('waits for the initial snapshot and uses the browser subprotocol', async () => {
    client.start();
    await flush();
    const socket = Socket.instances[0];
    expect(socket.protocol).toBe('falsepoker.v1');
    expect(socket.url).toContain('room_id=r_one');
    client.command('ready', { ready: true });
    expect(socket.sent).toHaveLength(0);
    socket.snapshot();
    expect(client.getSnapshot().status).toBe('connected');
  });
  it('waits for the accepted snapshot when ack comes first and guards repeated clicks', async () => {
    const socket = await connected();
    client.command('ready', { ready: true });
    client.command('ready', { ready: true });
    expect(socket.sent).toHaveLength(1);
    const command = JSON.parse(socket.sent[0]);
    socket.receive({
      type: 'ack',
      request_id: command.request_id,
      room_id: 'r_one',
      revision: '2',
      changed: true,
    });
    expect(client.getSnapshot().pending).toBe(true);
    socket.snapshot(room({ revision: '2' }));
    expect(client.getSnapshot().pending).toBe(false);
  });
  it('sends bid, challenge, and next-round intents with the observed context', async () => {
    source = room({
      revision: '23',
      game_id: '4',
      game: {
        phase: 'playing',
        round_phase: 'bidding',
        round_id: '12',
        elimination_limit: 6,
        starter_id: 'alice',
        next_starter_id: null,
        current_player_id: 'alice',
        players: [],
        winner_id: null,
        last_bid: null,
        last_result: null,
      },
    });
    const socket = await connected();
    const checkIntent = (action: string, data: object) => {
      const sent = JSON.parse(socket.sent.at(-1)!);
      expect(sent).toMatchObject({
        type: 'command',
        room_id: 'r_one',
        expected_revision: '23',
        game_id: '4',
        round_id: '12',
        action,
        data,
      });
      expect(sent.request_id).toMatch(/^[a-f0-9]{32}$/);
      expect(sent).not.toHaveProperty('player_id');
      socket.receive({
        type: 'error',
        request_id: sent.request_id,
        room_id: 'r_one',
        revision: '23',
        error: { code: 'OUT_OF_TURN', message: 'Wait', retryable: false },
      });
    };
    const bid = { bid: { category: 'high_card' as const, primary_rank: 'A' } };
    client.command('bid', bid);
    checkIntent('bid', bid);
    client.command('challenge', {});
    checkIntent('challenge', {});
    client.command('next_round', {});
    checkIntent('next_round', {});
    expect(socket.sent).toHaveLength(3);
  });
  it('never rolls back newer snapshots from older snapshots or acknowledgments', async () => {
    const socket = await connected();
    client.command('ready', { ready: true });
    const command = JSON.parse(socket.sent[0]);
    socket.snapshot(room({ revision: '9007199254740993' }));
    socket.snapshot(room({ revision: '9007199254740992' }));
    socket.receive({
      type: 'ack',
      request_id: command.request_id,
      room_id: 'r_one',
      revision: '2',
    });
    expect(client.getSnapshot().state?.revision).toBe('9007199254740993');
    expect(client.getSnapshot().pending).toBe(false);
  });
  it('restores a snapshot then retries the exact original command after loss', async () => {
    const socket = await connected();
    client.command('ready', { ready: true });
    const original = socket.sent[0];
    socket.drop();
    source = room({ revision: '3' });
    await vi.advanceTimersByTimeAsync(501);
    const replacement = Socket.instances.at(-1)!;
    expect(replacement).not.toBe(socket);
    expect(replacement.sent).toHaveLength(0);
    replacement.snapshot(source);
    expect(replacement.sent).toEqual([original]);
    socket.snapshot(room({ revision: '99' }));
    expect(client.getSnapshot().state?.revision).toBe('3');
  });
  it('cancels reconnect and ignores late messages during Strict Mode cleanup', async () => {
    const socket = await connected();
    socket.drop();
    client.stop();
    await vi.advanceTimersByTimeAsync(6000);
    expect(Socket.instances).toHaveLength(1);
    client.start();
    await flush();
    const replacement = Socket.instances.at(-1)!;
    socket.snapshot(room({ revision: '99' }));
    replacement.snapshot(room({ revision: '2' }));
    expect(client.getSnapshot().state?.revision).toBe('2');
    client.stop();
    expect(replacement.readyState).toBe(3);
    expect(vi.getTimerCount()).toBe(0);
  });
  it.each([
    'AUTH_REQUIRED',
    'SESSION_EXPIRED',
    'ROOM_UNAVAILABLE',
    'ORIGIN_NOT_ALLOWED',
  ])('stops retrying terminal HTTP error %s', async (code) => {
    vi.mocked(roomInfo).mockRejectedValue(new ApiError(code, 'Unavailable'));
    client.start();
    await flush();
    await vi.advanceTimersByTimeAsync(20_000);
    expect(client.getSnapshot().status).toBe('unavailable');
    expect(Socket.instances).toHaveLength(0);
  });
  it('probes HTTP again after an opaque handshake failure', async () => {
    client.start();
    await flush();
    Socket.instances[0].drop();
    vi.mocked(roomInfo).mockRejectedValue(
      new ApiError('ROOM_UNAVAILABLE', 'Unavailable'),
    );
    await vi.advanceTimersByTimeAsync(501);
    expect(client.getSnapshot().status).toBe('unavailable');
  });
  it('retries transient HTTP errors without joining or replacing identity', async () => {
    vi.mocked(roomInfo).mockRejectedValueOnce(
      new ApiError('NETWORK', 'Offline', true),
    );
    client.start();
    await flush();
    expect(client.getSnapshot().status).toBe('reconnecting');
    await vi.advanceTimersByTimeAsync(501);
    expect(Socket.instances).toHaveLength(1);
  });
  it('refreshes stale errors without reinterpreting the old intent', async () => {
    const socket = await connected();
    client.command('ready', { ready: true });
    const command = JSON.parse(socket.sent[0]);
    source = room({ revision: '2' });
    socket.receive({
      type: 'error',
      request_id: command.request_id,
      room_id: 'r_one',
      revision: '2',
      error: { code: 'STALE_STATE', message: 'Review', retryable: false },
    });
    await flush();
    await vi.advanceTimersByTimeAsync(20_000);
    expect(socket.sent).toHaveLength(1);
    expect(client.getSnapshot().state?.revision).toBe('2');
    expect(client.getSnapshot().pending).toBe(false);
  });
  it('bounds uncertain-command retries before the server retention ends', async () => {
    const socket = await connected();
    client.command('ready', { ready: true });
    await vi.advanceTimersByTimeAsync(240_001);
    const count = socket.sent.length;
    expect(client.getSnapshot().errorCode).toBe('UNCERTAIN');
    await vi.advanceTimersByTimeAsync(30_000);
    expect(socket.sent).toHaveLength(count);
  });
  it('stops protocol-rejected connections without suggesting the room disappeared', async () => {
    const socket = await connected();
    socket.drop(1008);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(client.getSnapshot().errorCode).toBe('PROTOCOL');
    expect(Socket.instances).toHaveLength(1);
  });
  it('refuses to mix a reused room code with a new lifetime', async () => {
    const socket = await connected();
    socket.drop();
    source = room({ room_id: 'r_replaced' });
    await vi.advanceTimersByTimeAsync(501);
    expect(client.getSnapshot().errorCode).toBe('STALE_ROOM');
    expect(client.getSnapshot().state).toBeNull();
  });
});
