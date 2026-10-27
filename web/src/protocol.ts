// Transport DTOs mirror docs/protocol.md. Counters stay strings on the wire.
export interface Member {
  player_id: string;
  display_name: string;
  ready: boolean;
}
export interface Card {
  rank: string;
  suit: string;
}
export interface Player {
  player_id: string;
  display_name: string;
  required_hand_size: number;
  hand_count: number;
  eliminated: boolean;
  withdrawn: boolean;
  hand: Card[] | null;
}
export type BidCategory =
  | 'high_card'
  | 'pair'
  | 'two_pairs'
  | 'low_straight'
  | 'high_straight'
  | 'three_of_a_kind'
  | 'full_house'
  | 'flush'
  | 'four_of_a_kind'
  | 'straight_flush'
  | 'royal_straight_flush';
export interface Bid {
  category: BidCategory;
  primary_rank?: string;
  secondary_rank?: string;
  suit?: string;
}
export interface PlacedBid {
  player_id: string;
  bid: Bid;
}
export interface RevealedHand {
  player_id: string;
  cards: Card[];
}
export interface RoundResult {
  round_id: string;
  elimination_limit: number;
  challenged_bid: PlacedBid;
  challenger_id: string;
  claim_satisfied: boolean;
  loser_id: string;
  revealed_hands: RevealedHand[];
}
export interface Game {
  phase: 'playing' | 'finished';
  round_phase: 'bidding' | 'resolution';
  round_id: string;
  elimination_limit: number;
  starter_id: string;
  next_starter_id: string | null;
  current_player_id: string | null;
  players: Player[];
  winner_id: string | null;
  last_bid: PlacedBid | null;
  last_result: RoundResult | null;
}
export interface RoomState {
  room_id: string;
  room_code: string;
  revision: string;
  game_id: string;
  recipient_id: string;
  admin_id: string;
  role: 'participant' | 'spectator';
  members: Member[];
  game: Game | null;
}
export interface Membership {
  room_code: string;
  room_id: string;
  player_id: string;
  revision: string;
}
export interface Session {
  protocol_version: number;
  player_id: string;
}
export interface StartData {
  seat_order: string[] | null;
  shuffle_seats: boolean;
  first_bidder: string | null;
}
export interface Command {
  type: 'command';
  request_id: string;
  room_id: string;
  expected_revision: string;
  game_id: string;
  round_id: string;
  action: 'ready' | 'start' | 'bid' | 'challenge' | 'next_round';
  data: { ready: boolean } | StartData | { bid: Bid } | Record<string, never>;
}
export interface Failure {
  code: string;
  message: string;
  retryable: boolean;
}
export type Message =
  | { type: 'snapshot'; state: RoomState }
  | {
      type: 'ack';
      request_id: string;
      room_id: string;
      revision: string;
      changed: boolean;
      replayed: boolean;
    }
  | {
      type: 'error';
      request_id: string;
      room_id: string;
      revision: string;
      error: Failure;
      replayed: boolean;
    };

export function counter(value: string): bigint {
  if (typeof value !== 'string' || !/^(0|[1-9][0-9]*)$/.test(value))
    throw new Error('Invalid protocol counter');
  const result = BigInt(value);
  if (result > 18446744073709551615n)
    throw new Error('Invalid protocol counter');
  return result;
}
export function requestID(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (x) =>
    x.toString(16).padStart(2, '0'),
  ).join('');
}
export function normalizeCode(value: string): string {
  return value.trim().toLowerCase();
}
export function validCode(value: string): boolean {
  return /^[a-f0-9]{12}$/.test(normalizeCode(value));
}
export function nameError(value: string): string | null {
  const name = value.trim();
  if (!name) return 'Enter a display name.';
  if (Array.from(name).length > 32) return 'Use 32 characters or fewer.';
  if (/\p{Cc}/u.test(name))
    return 'Your name cannot contain control characters.';
  return null;
}
export function roomPath(code: string): string {
  return `/room/${normalizeCode(code)}`;
}
export function pathCode(path: string): string | null {
  return /^\/room\/([a-fA-F0-9]{12})\/?$/.exec(path)?.[1].toLowerCase() ?? null;
}
export function checkedState(state: RoomState): RoomState {
  counter(state.revision);
  counter(state.game_id);
  if (state.game) counter(state.game.round_id);
  if (
    !state.room_id ||
    !validCode(state.room_code) ||
    !state.recipient_id ||
    !Array.isArray(state.members) ||
    !state.members.some((m) => m.player_id === state.recipient_id) ||
    !state.members.some((m) => m.player_id === state.admin_id) ||
    !['participant', 'spectator'].includes(state.role)
  )
    throw new Error('Invalid server snapshot');
  return state;
}
