import type { components } from "../../shared/api/schema";

export type GameState = components["schemas"]["GameState"];
export type GameResultReason = components["schemas"]["GameResultReason"];
export type Game = components["schemas"]["Game"];

export interface GameView {
  id: string;
  slotId: string;
  attemptNo: number;
  state: GameState;
  resultReason: GameResultReason | null;
  winnerId: string | null;
  resultRevisionId: string | null;
}
