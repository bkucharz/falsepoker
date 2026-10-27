import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Gameplay } from './Gameplay';
import type { RoomConnection } from './connection';
import type { RoomState } from './protocol';
import { room } from './testing/fixtures';

const base = (): RoomState =>
  room({
    revision: '9',
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
      players: [
        {
          player_id: 'alice',
          display_name: 'Alice',
          required_hand_size: 1,
          hand_count: 1,
          eliminated: false,
          withdrawn: false,
          hand: [{ rank: '9', suit: 'clubs' }],
        },
        {
          player_id: 'bob',
          display_name: 'Bob',
          required_hand_size: 1,
          hand_count: 1,
          eliminated: false,
          withdrawn: false,
          hand: null,
        },
        {
          player_id: 'cara',
          display_name: 'Cara',
          required_hand_size: 1,
          hand_count: 1,
          eliminated: false,
          withdrawn: false,
          hand: null,
        },
      ],
      winner_id: null,
      last_bid: null,
      last_result: null,
    },
  });
const command = () => vi.fn() as RoomConnection['command'];

beforeEach(() => {
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    value: 'visible',
  });
});

describe('gameplay table', () => {
  it('shows the player hand, opponents, current turn, and guided claim entry', async () => {
    const user = userEvent.setup();
    const send = command();
    render(<Gameplay state={base()} pending={false} command={send} />);

    expect(
      screen.getByRole('region', { name: 'False Poker game table' }),
    ).toBeInTheDocument();
    expect(screen.getByRole('img', { name: '9 of Clubs' })).toBeInTheDocument();
    expect(screen.getByLabelText('Bob, 1 card')).toBeInTheDocument();
    expect(screen.getByText('YOUR TURN')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Make a claim' }));
    await user.selectOptions(
      screen.getByLabelText('Combination'),
      'full_house',
    );
    await user.selectOptions(screen.getByLabelText('Three of a kind'), 'K');
    await user.selectOptions(screen.getByLabelText('Pair'), 'Q');
    await user.click(screen.getByRole('button', { name: /Place claim/ }));
    expect(send).toHaveBeenCalledWith('bid', {
      bid: { category: 'full_house', primary_rank: 'K', secondary_rank: 'Q' },
    });
  });

  it.each([
    ['high_card', ['Rank']],
    ['pair', ['Rank']],
    ['two_pairs', ['Higher pair', 'Lower pair']],
    ['low_straight', []],
    ['high_straight', []],
    ['three_of_a_kind', ['Rank']],
    ['full_house', ['Three of a kind', 'Pair']],
    ['flush', ['Suit']],
    ['four_of_a_kind', ['Rank']],
    ['straight_flush', ['Suit']],
    ['royal_straight_flush', ['Suit']],
  ] as const)(
    'shows the applicable fields for %s',
    async (category, fields) => {
      const user = userEvent.setup();
      render(<Gameplay state={base()} pending={false} command={command()} />);
      await user.click(screen.getByRole('button', { name: 'Make a claim' }));
      await user.selectOptions(screen.getByLabelText('Combination'), category);
      for (const label of [
        'Rank',
        'Higher pair',
        'Lower pair',
        'Three of a kind',
        'Pair',
        'Suit',
      ]) {
        if ((fields as readonly string[]).includes(label))
          expect(screen.getByLabelText(label)).toBeInTheDocument();
        else expect(screen.queryByLabelText(label)).not.toBeInTheDocument();
      }
    },
  );

  it('offers challenge only for the current player when a claim exists', async () => {
    const state = base();
    state.game!.last_bid = {
      player_id: 'bob',
      bid: { category: 'pair', primary_rank: 'Q' },
    };
    const send = command();
    const { rerender } = render(
      <Gameplay state={state} pending={false} command={send} />,
    );
    expect(screen.getByText('Pair: Q')).toBeInTheDocument();
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Challenge' }));
    expect(send).toHaveBeenCalledWith('challenge', {});
    rerender(
      <Gameplay
        state={{ ...state, game: { ...state.game!, current_player_id: 'bob' } }}
        pending={false}
        command={send}
      />,
    );
    expect(
      screen.queryByRole('button', { name: 'Challenge' }),
    ).not.toBeInTheDocument();
  });

  it('disables actions while disconnected or while a command is pending', () => {
    const state = base();
    state.game!.last_bid = {
      player_id: 'bob',
      bid: { category: 'pair', primary_rank: 'Q' },
    };
    const { rerender } = render(
      <Gameplay
        state={state}
        connected={false}
        pending={false}
        command={command()}
      />,
    );
    expect(screen.getByRole('button', { name: 'Raise claim' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Challenge' })).toBeDisabled();
    rerender(<Gameplay state={state} connected pending command={command()} />);
    expect(screen.getByRole('button', { name: 'Sending…' })).toBeDisabled();
  });

  it('lets spectators peek only while holding an individual seat or the settings control', async () => {
    const state = base();
    state.role = 'spectator';
    state.game!.players[0].hand = [{ rank: '9', suit: 'clubs' }];
    state.game!.players[1].hand = [{ rank: '10', suit: 'hearts' }];
    const { rerender } = render(
      <Gameplay state={state} pending={false} command={command()} />,
    );
    expect(
      screen.queryByRole('img', { name: '10 of Hearts' }),
    ).not.toBeInTheDocument();
    const bob = screen.getByRole('button', { name: /Bob, 1 card/ });
    fireEvent.pointerDown(bob, { button: 0 });
    expect(
      screen.getByRole('img', { name: '10 of Hearts' }),
    ).toBeInTheDocument();
    fireEvent.pointerUp(bob);
    expect(
      screen.queryByRole('img', { name: '10 of Hearts' }),
    ).not.toBeInTheDocument();
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Open game settings' }));
    const all = screen.getByRole('button', {
      name: 'Hold to peek at all hands',
    });
    fireEvent.pointerDown(all, { button: 0 });
    expect(screen.getByRole('img', { name: '9 of Clubs' })).toBeInTheDocument();
    fireEvent.pointerUp(all);
    expect(
      screen.queryByRole('img', { name: '9 of Clubs' }),
    ).not.toBeInTheDocument();
    fireEvent.pointerDown(bob, { button: 0 });
    rerender(
      <Gameplay
        state={{ ...state, revision: '10' }}
        pending={false}
        command={command()}
      />,
    );
    expect(
      screen.queryByRole('img', { name: '10 of Hearts' }),
    ).not.toBeInTheDocument();
  });

  it('reveals the round result and gives next round only to the designated starter', async () => {
    const state = base();
    state.game!.round_phase = 'resolution';
    state.game!.current_player_id = null;
    state.game!.next_starter_id = 'bob';
    state.game!.players[1].hand = [{ rank: '10', suit: 'hearts' }];
    state.game!.last_result = {
      round_id: '1',
      elimination_limit: 6,
      challenged_bid: {
        player_id: 'alice',
        bid: { category: 'flush', suit: 'clubs' },
      },
      challenger_id: 'bob',
      claim_satisfied: false,
      loser_id: 'alice',
      revealed_hands: [
        { player_id: 'alice', cards: [{ rank: '9', suit: 'clubs' }] },
        { player_id: 'bob', cards: [{ rank: '10', suit: 'hearts' }] },
      ],
    };
    const send = command();
    const { rerender } = render(
      <Gameplay state={state} pending={false} command={send} />,
    );
    expect(
      screen.getByRole('heading', { name: 'The claim was false' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('img', { name: '10 of Hearts' }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Next round' }),
    ).not.toBeInTheDocument();
    rerender(
      <Gameplay
        state={{ ...state, recipient_id: 'bob' }}
        pending={false}
        command={send}
      />,
    );
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Next round' }));
    expect(send).toHaveBeenCalledWith('next_round', {});
  });

  it('shows a winner without game actions and keeps room controls in settings', async () => {
    const state = base();
    state.game!.phase = 'finished';
    state.game!.round_phase = 'resolution';
    state.game!.winner_id = 'bob';
    state.game!.next_starter_id = null;
    render(<Gameplay state={state} pending={false} command={command()} />);
    expect(
      screen.getByRole('heading', { name: 'Bob wins' }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Next round' }),
    ).not.toBeInTheDocument();
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Open game settings' }));
    expect(
      screen.getByRole('dialog', { name: 'Settings' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Leave room' }),
    ).toBeInTheDocument();
  });
});
