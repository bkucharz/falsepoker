import { ApiError, roomInfo, terminalCodes } from './api';
import {
  checkedState,
  counter,
  requestID,
  type Command,
  type Message,
  type RoomState,
  type StartData,
} from './protocol';

export type ConnectionStatus =
  'connecting' | 'connected' | 'reconnecting' | 'unavailable';
export interface ConnectionView {
  status: ConnectionStatus;
  state: RoomState | null;
  pending: boolean;
  error: string | null;
  errorCode: string | null;
}
interface Pending {
  command: Command;
  started: number;
  ackRevision?: string;
}
const terminalCloses = new Set([
  1002, 1003, 1007, 1008, 1009, 4001, 4003, 4004,
]);

// One controller owns one tab's subscription. It does not own membership: stop()
// releases resources, whereas explicit HTTP leave is a distinct user action.
export class RoomConnection {
  private view: ConnectionView = {
    status: 'connecting',
    state: null,
    pending: false,
    error: null,
    errorCode: null,
  };
  private listeners = new Set<() => void>();
  private socket?: WebSocket;
  private abort?: AbortController;
  private reconnectTimer?: ReturnType<typeof setTimeout>;
  private commandTimer?: ReturnType<typeof setTimeout>;
  private handshakeTimer?: ReturnType<typeof setTimeout>;
  private generation = 0;
  private running = false;
  private failures = 0;
  private intent?: Pending;
  private roomID?: string;
  constructor(
    private code: string,
    roomID?: string,
  ) {
    this.roomID = roomID;
  }
  getSnapshot = () => this.view;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  private update(patch: Partial<ConnectionView>) {
    this.view = { ...this.view, ...patch };
    this.listeners.forEach((listener) => listener());
  }
  start() {
    if (this.running) return;
    this.running = true;
    this.update({ status: this.view.state ? 'reconnecting' : 'connecting' });
    void this.connect();
  }
  stop() {
    this.running = false;
    this.generation++;
    this.abort?.abort();
    clearTimeout(this.reconnectTimer);
    clearTimeout(this.commandTimer);
    clearTimeout(this.handshakeTimer);
    this.socket?.close();
    this.socket = undefined;
  }
  private current(generation: number) {
    return this.running && generation === this.generation;
  }
  private unavailable(code: string, message: string) {
    this.stop();
    this.intent = undefined;
    this.update({
      status: 'unavailable',
      state: null,
      pending: false,
      errorCode: code,
      error: message,
    });
  }
  private retry() {
    if (!this.running) return;
    this.update({ status: 'reconnecting' });
    clearTimeout(this.reconnectTimer);
    const delay =
      Math.min(500 * 2 ** Math.min(this.failures++, 4), 5000) *
      (0.8 + Math.random() * 0.2);
    this.reconnectTimer = setTimeout(() => void this.connect(), delay);
  }
  private async connect() {
    if (!this.running) return;
    const generation = ++this.generation;
    this.abort?.abort();
    this.abort = new AbortController();
    // Fetch also classifies errors hidden by native WebSocket handshakes. Every
    // reconnect checks membership; it never joins or recreates the session.
    try {
      const { state } = await roomInfo(
        this.code,
        AbortSignal.any([this.abort.signal, AbortSignal.timeout(10_000)]),
      );
      if (!this.current(generation)) return;
      checkedState(state);
      if (this.roomID && this.roomID !== state.room_id) {
        this.unavailable(
          'STALE_ROOM',
          'This invitation now refers to a different room. Join it explicitly to continue.',
        );
        return;
      }
      this.roomID = state.room_id;
      const url = new URL(`/api/v1/rooms/${this.code}/ws`, location.href);
      url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
      url.searchParams.set('room_id', this.roomID);
      const socket = new WebSocket(url, 'falsepoker.v1');
      this.socket = socket;
      let baseline = false;
      this.handshakeTimer = setTimeout(() => {
        if (this.current(generation)) socket.close();
      }, 10_000);
      socket.onmessage = (event) => {
        if (!this.current(generation)) return;
        try {
          const message: Message = JSON.parse(event.data);
          if (!baseline && message.type !== 'snapshot')
            throw new Error('Missing initial snapshot');
          if (message.type === 'snapshot') {
            const state = checkedState(message.state);
            if (state.room_id !== this.roomID || state.room_code !== this.code)
              throw new Error('Wrong room snapshot');
            if (
              this.view.state &&
              state.recipient_id !== this.view.state.recipient_id
            )
              throw new Error('Wrong recipient');
            clearTimeout(this.handshakeTimer);
            const first = !baseline;
            baseline = true;
            this.failures = 0;
            // Full snapshots can skip revisions. Never roll back a newer view.
            if (
              !this.view.state ||
              counter(state.revision) > counter(this.view.state.revision)
            ) {
              this.update({ state });
            }
            this.update({ status: 'connected' });
            this.finishAcknowledged();
            if (first) this.sendPending();
          } else if (message.type === 'ack' || message.type === 'error') {
            if (message.room_id !== this.roomID)
              throw new Error('Wrong outcome room');
            counter(message.revision);
            if (message.request_id !== this.intent?.command.request_id) return;
            if (message.type === 'ack') {
              this.intent.ackRevision = message.revision;
              this.finishAcknowledged();
            } else {
              this.intent = undefined;
              clearTimeout(this.commandTimer);
              this.update({
                pending: false,
                error: message.error.message,
                errorCode: message.error.code,
              });
              if (terminalCodes.has(message.error.code))
                this.unavailable(message.error.code, message.error.message);
              else if (message.error.code.startsWith('STALE_'))
                void this.refresh(generation);
            }
          } else throw new Error('Unsupported server message');
        } catch {
          this.unavailable(
            'PROTOCOL',
            'The server sent an unsupported response. Reload after checking the server version.',
          );
        }
      };
      socket.onclose = (event) => {
        if (!this.current(generation)) return;
        clearTimeout(this.handshakeTimer);
        clearTimeout(this.commandTimer);
        this.generation++;
        if (terminalCloses.has(event.code)) {
          const message =
            event.code === 4001
              ? 'Your session is no longer available. Start a new session to continue.'
              : event.code === 4003 || event.code === 4004
                ? 'Your room or membership is no longer available. You can join again.'
                : 'The connection was rejected. Reload before trying again.';
          this.unavailable(
            event.code === 4001
              ? 'SESSION_EXPIRED'
              : event.code === 4003 || event.code === 4004
                ? 'ROOM_UNAVAILABLE'
                : 'PROTOCOL',
            message,
          );
        } else this.retry();
      };
      // Browser errors have no usable HTTP details. onclose triggers an HTTP
      // membership probe on the next attempt, including for unexpected code 1000.
      socket.onerror = () => {};
    } catch (error) {
      if (!this.current(generation)) return;
      if (error instanceof ApiError && terminalCodes.has(error.code)) {
        this.unavailable(
          error.code,
          error.code === 'ROOM_UNAVAILABLE'
            ? 'This room or your membership is no longer available. You can join again.'
            : error.message,
        );
      } else if (
        error instanceof ApiError &&
        (error.retryable || error.code === 'NETWORK')
      )
        this.retry();
      else
        this.unavailable(
          'PROTOCOL',
          error instanceof Error ? error.message : 'Unable to connect.',
        );
    }
  }
  private async refresh(generation: number) {
    try {
      const { state } = await roomInfo(this.code);
      if (!this.current(generation)) return;
      checkedState(state);
      if (
        state.room_id === this.roomID &&
        (!this.view.state ||
          counter(state.revision) > counter(this.view.state.revision))
      )
        this.update({ state });
    } catch (error) {
      if (
        this.current(generation) &&
        error instanceof ApiError &&
        terminalCodes.has(error.code)
      )
        this.unavailable(error.code, error.message);
    }
  }
  command(action: 'ready', data: { ready: boolean }): void;
  command(action: 'start', data: StartData): void;
  command(action: Command['action'], data: Command['data']) {
    const state = this.view.state;
    if (!state || this.view.status !== 'connected' || this.intent) return;
    this.intent = {
      started: performance.now(),
      command: {
        type: 'command',
        request_id: requestID(),
        room_id: state.room_id,
        expected_revision: state.revision,
        game_id: state.game_id,
        round_id: state.game?.round_id ?? '0',
        action,
        data,
      },
    };
    this.update({ pending: true, error: null, errorCode: null });
    this.sendPending();
  }
  private sendPending() {
    clearTimeout(this.commandTimer);
    if (!this.intent || this.view.status !== 'connected') return;
    if (performance.now() - this.intent.started >= 240_000) {
      this.intent = undefined;
      this.update({
        pending: false,
        errorCode: 'UNCERTAIN',
        error:
          'The action outcome is uncertain. Review the latest room state before acting again.',
      });
      return;
    }
    if (this.socket?.readyState === WebSocket.OPEN) {
      try {
        this.socket.send(JSON.stringify(this.intent.command));
      } catch {
        this.socket.close();
      }
    }
    this.commandTimer = setTimeout(() => this.sendPending(), 10_000);
  }
  private finishAcknowledged() {
    if (
      this.intent?.ackRevision &&
      this.view.state &&
      counter(this.view.state.revision) >= counter(this.intent.ackRevision)
    ) {
      this.intent = undefined;
      clearTimeout(this.commandTimer);
      this.update({ pending: false });
    }
  }
}
