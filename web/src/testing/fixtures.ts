import type { RoomState } from '../protocol';
export const room = (patch: Partial<RoomState> = {}): RoomState => ({
  room_id: 'r_one',
  room_code: 'a1b2c3d4e5f6',
  revision: '1',
  game_id: '0',
  recipient_id: 'alice',
  admin_id: 'alice',
  role: 'spectator',
  game: null,
  members: [
    { player_id: 'alice', display_name: 'Alice', ready: false },
    { player_id: 'bob', display_name: 'Bob', ready: false },
  ],
  ...patch,
});
