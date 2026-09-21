import type { components } from "../../shared/api/schema";

import type { TournamentFormat } from "./format";
import type { GameView } from "./game";

export type SeriesState = components["schemas"]["SeriesState"];
export type TournamentCategory = components["schemas"]["Category"];
export type Series = components["schemas"]["Series"];
export type GameSlot = components["schemas"]["GameSlot"];

export interface SeriesScoreView {
  firstWins: number;
  secondWins: number;
}

export interface PublicSeriesView {
  seriesId: string;
  format: TournamentFormat;
  state: SeriesState;
  firstDisplayName: string;
  secondDisplayName: string;
  score: SeriesScoreView;
  currentGamePosition: number | null;
  stage: components["schemas"]["PublicLiveSeries"]["stage"];
  roundNumber: number | null;
  scheduledAt: string | null;
}

export interface ParticipantSeriesView {
  seriesId: string;
  tournamentId: string;
  firstParticipantId: string;
  secondParticipantId: string;
  format: TournamentFormat;
  state: SeriesState;
  score: SeriesScoreView;
  winnerId: string | null;
  slots: readonly SeriesSlotView[];
}

export interface OperatorSeriesView {
  seriesId: string;
  tournamentId: string;
  firstParticipantId: string;
  secondParticipantId: string;
  format: TournamentFormat;
  state: SeriesState;
  score: SeriesScoreView;
  winnerId: string | null;
  slots: readonly SeriesSlotView[];
}

export interface SeriesSlotView {
  slotId: string;
  seriesId: string;
  position: number;
  category: TournamentCategory;
  scoreBefore: SeriesScoreView;
  attempts: readonly GameView[];
}
