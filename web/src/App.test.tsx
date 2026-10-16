import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Lobby } from './Lobby';
import { Welcome } from './Welcome';
import type { RoomConnection, ConnectionView } from './connection';
import { room } from './testing/fixtures';
import type { RoomState } from './protocol';
function view(state = room()): ConnectionView {
  return {
    status: 'connected',
    state,
    pending: false,
    error: null,
    errorCode: null,
  };
}
const playing = (): RoomState =>
  room({
    role: 'participant',
    game_id: '1',
    game: {
      phase: 'playing',
      round_phase: 'bidding',
      round_id: '1',
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
beforeEach(() => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true,
    value: function () {
      this.setAttribute('open', '');
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, 'close', {
    configurable: true,
    value: function () {
      this.removeAttribute('open');
    },
  });
});
describe('room entry', () => {
  it('validates names and codes before submitting', async () => {
    const user = userEvent.setup();
    render(<Welcome onJoined={vi.fn()} />);
    await user.click(
      screen.getByRole('button', { name: /Create private room/ }),
    );
    expect(screen.getByRole('alert')).toHaveTextContent('Enter a display name');
    await user.type(screen.getByLabelText('Your display name'), 'Alice');
    await user.click(screen.getByRole('button', { name: 'Join a room' }));
    await user.type(screen.getByLabelText('Room code'), 'wrong');
    await user.click(screen.getByRole('button', { name: /^Join room/ }));
    expect(screen.getByRole('alert')).toHaveTextContent('12-character');
  });
  it('prefills invitation joins and displays server rejection', async () => {
    const user = userEvent.setup();
    vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: false,
      json: async () => ({
        error: {
          code: 'ROOM_UNAVAILABLE',
          message: 'Room unavailable.',
          retryable: false,
        },
      }),
    } as Response);
    render(<Welcome invitation="a1b2c3d4e5f6" onJoined={vi.fn()} />);
    expect(screen.getByLabelText('Room code')).toHaveValue('a1b2c3d4e5f6');
    await user.type(screen.getByLabelText('Your display name'), 'Alice');
    await user.click(screen.getByRole('button', { name: /^Join room/ }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Room unavailable',
    );
  });
});
describe('lobby permissions and choices', () => {
  it('only permits admin starts, independently of participation', () => {
    const state = room({
      members: room().members.map((m) => ({ ...m, ready: true })),
    });
    const { rerender } = render(
      <Lobby view={view(state)} command={vi.fn()} onLeave={vi.fn()} />,
    );
    expect(screen.getByRole('button', { name: /Start game/ })).toBeEnabled();
    rerender(
      <Lobby
        view={view({ ...state, admin_id: 'bob' })}
        command={vi.fn()}
        onLeave={vi.fn()}
      />,
    );
    expect(
      screen.queryByRole('button', { name: /Start game/ }),
    ).not.toBeInTheDocument();
  });
  it('submits independent seat order and first bidder choices', async () => {
    const user = userEvent.setup(),
      command = vi.fn();
    const state = room({
      members: room().members.map((m) => ({ ...m, ready: true })),
    });
    render(
      <Lobby
        view={view(state)}
        command={command as RoomConnection['command']}
        onLeave={vi.fn()}
      />,
    );
    await user.selectOptions(
      screen.getByRole('combobox', { name: 'Seat order' }),
      'manual',
    );
    await user.click(screen.getByRole('button', { name: 'Move Bob up' }));
    await user.selectOptions(screen.getByLabelText('First bidder'), 'alice');
    await user.click(screen.getByRole('button', { name: /Start game/ }));
    expect(command).toHaveBeenCalledWith('start', {
      seat_order: ['bob', 'alice'],
      shuffle_seats: false,
      first_bidder: 'alice',
    });
  });
  it('requires review when the explicit ready roster changes', async () => {
    const user = userEvent.setup(),
      command = vi.fn();
    const state = room({
      members: room().members.map((m) => ({ ...m, ready: true })),
    });
    const { rerender } = render(
      <Lobby view={view(state)} command={command} onLeave={vi.fn()} />,
    );
    await user.selectOptions(
      screen.getByRole('combobox', { name: 'Seat order' }),
      'manual',
    );
    rerender(
      <Lobby
        view={view({
          ...state,
          members: [
            ...state.members,
            { player_id: 'carol', display_name: 'Carol', ready: true },
          ],
        })}
        command={command}
        onLeave={vi.fn()}
      />,
    );
    expect(screen.getByRole('button', { name: /Start game/ })).toBeDisabled();
    await user.click(
      screen.getByRole('button', { name: 'Confirm updated choices' }),
    );
    expect(screen.getByRole('button', { name: /Start game/ })).toBeEnabled();
  });
  it('guards starts outside 2–24 participants and while disconnected/pending', () => {
    const command = vi.fn();
    const { rerender } = render(
      <Lobby view={view()} command={command} onLeave={vi.fn()} />,
    );
    expect(screen.getByRole('button', { name: /Start game/ })).toBeDisabled();
    const many = room({
      members: Array.from({ length: 25 }, (_, i) => ({
        player_id: i === 0 ? 'alice' : `p_${i}`,
        display_name: `Player ${i}`,
        ready: true,
      })),
    });
    rerender(<Lobby view={view(many)} command={command} onLeave={vi.fn()} />);
    expect(screen.getByRole('button', { name: /Start game/ })).toBeDisabled();
    rerender(
      <Lobby
        view={{ ...view(), status: 'reconnecting' }}
        command={command}
        onLeave={vi.fn()}
      />,
    );
    expect(screen.getByRole('button', { name: 'I’m ready' })).toBeDisabled();
    rerender(
      <Lobby
        view={{ ...view(), pending: true }}
        command={command}
        onLeave={vi.fn()}
      />,
    );
    expect(screen.getByRole('button', { name: 'Updating…' })).toBeDisabled();
  });
  it('requires confirmation only when an active participant leaves', async () => {
    const user = userEvent.setup(),
      leave = vi.fn();
    const { rerender } = render(
      <Lobby view={view(playing())} command={vi.fn()} onLeave={leave} />,
    );
    await user.click(screen.getByRole('button', { name: 'Leave room' }));
    expect(screen.getByRole('dialog')).toBeVisible();
    expect(leave).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Stay in room' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Leave room' }));
    await user.click(screen.getByRole('button', { name: 'Leave game' }));
    expect(leave).toHaveBeenCalledOnce();
    rerender(
      <Lobby
        view={view({ ...playing(), role: 'spectator' })}
        command={vi.fn()}
        onLeave={leave}
      />,
    );
    await user.click(screen.getByRole('button', { name: 'Leave room' }));
    expect(leave).toHaveBeenCalledTimes(2);
  });
  it('renders names as text and exposes no player presence or private cards', () => {
    const state = room({
      members: [
        {
          player_id: 'alice',
          display_name: '<img src=x onerror=alert(1)>',
          ready: true,
        },
      ],
    });
    render(<Lobby view={view(state)} command={vi.fn()} onLeave={vi.fn()} />);
    expect(
      screen.getAllByText('<img src=x onerror=alert(1)>').length,
    ).toBeGreaterThan(0);
    expect(document.querySelector('img')).toBeNull();
    expect(screen.queryByText(/online|offline/i)).not.toBeInTheDocument();
  });
});
