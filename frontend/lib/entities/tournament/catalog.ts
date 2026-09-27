import type { components } from "../../shared/api/schema";
import {
  formatArenaDateTime,
  formatTournamentState,
} from "../../shared/lib";

export type PublicTournamentCatalogItemResponse =
  components["schemas"]["PublicTournamentCatalogItem"];

export type PublicTournamentCatalogGroup = "live" | "upcoming" | "completed";
export type PublicTournamentCatalogStage =
  | "registration"
  | "roster_locked"
  | "swiss"
  | "golden"
  | "playoffs"
  | "completed"
  | "cancelled";

export type PublicTournamentCatalogItemView = Readonly<{
  tournamentId: string;
  publicId: string;
  name: string;
  preset: components["schemas"]["TournamentPreset"];
  state: components["schemas"]["TournamentState"];
  group: PublicTournamentCatalogGroup;
  stage: PublicTournamentCatalogStage;
  plannedRosterSize: number;
  rosterSize: number;
  createdAt: string;
  startedAt: string | null;
  finishedAt: string | null;
  scheduledAt: string | null;
}>;

export const toPublicTournamentCatalogItemView = (
  item: PublicTournamentCatalogItemResponse,
): PublicTournamentCatalogItemView => ({
  tournamentId: item.tournament_id,
  publicId: item.public_id,
  name: item.name,
  preset: item.preset,
  state: item.state,
  group: item.group,
  stage: item.stage,
  plannedRosterSize: item.planned_roster_size,
  rosterSize: item.roster_size,
  createdAt: item.created_at,
  startedAt: item.started_at,
  finishedAt: item.finished_at,
  scheduledAt: item.scheduled_at,
});

const GROUP_LABELS: Readonly<Record<PublicTournamentCatalogGroup, string>> = {
  live: "Идет",
  upcoming: "Предстоящее",
  completed: "Завершено",
};

const STAGE_LABELS: Readonly<Record<PublicTournamentCatalogStage, string>> = {
  registration: "Регистрация",
  roster_locked: "Состав зафиксирован",
  swiss: "Квалификация",
  golden: "Квалификация",
  playoffs: "Плей-офф",
  completed: "Завершено",
  cancelled: "Отменено",
};

export const catalogGroupLabel = (group: PublicTournamentCatalogGroup): string =>
  GROUP_LABELS[group];

export const catalogStageLabel = (stage: PublicTournamentCatalogStage): string =>
  STAGE_LABELS[stage];

export const catalogStateLabel = (
  state: PublicTournamentCatalogItemView["state"],
): string => formatTournamentState(state);

export const catalogFormatLabel = (
  preset: PublicTournamentCatalogItemView["preset"],
): string => preset === "tournament_v1" ? "Квалификация и плей-офф" : "Формат соревнования";

export const catalogRosterLabel = (
  item: Pick<PublicTournamentCatalogItemView, "rosterSize" | "plannedRosterSize">,
): string => `${item.rosterSize} из ${item.plannedRosterSize}`;

export const catalogScheduleLabel = (
  item: Pick<PublicTournamentCatalogItemView, "scheduledAt" | "startedAt" | "finishedAt">,
): string => {
  if (item.scheduledAt) {
    return `Старт: ${formatArenaDateTime(item.scheduledAt)}`;
  }
  if (item.startedAt) {
    return `Начато: ${formatArenaDateTime(item.startedAt)}`;
  }
  if (item.finishedAt) {
    return `Завершено: ${formatArenaDateTime(item.finishedAt)}`;
  }
  return "Время уточняется";
};

export const catalogCreatedLabel = (
  createdAt: PublicTournamentCatalogItemView["createdAt"],
): string => formatArenaDateTime(createdAt);
