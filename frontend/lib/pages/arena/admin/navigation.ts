import type { TournamentAdminView } from "../../../widgets/tournament-admin";

export type { TournamentAdminView } from "../../../widgets/tournament-admin";

export type AdminSection = "players" | "tasks" | "tournaments" | "audit";

export interface AdminNavigation {
  section: AdminSection;
  tournamentId: string | null;
  view: TournamentAdminView;
}

export const DEFAULT_ADMIN_NAVIGATION: AdminNavigation = {
  section: "tournaments",
  tournamentId: null,
  view: "overview",
};

const ADMIN_SECTIONS: ReadonlySet<string> = new Set([
  "players",
  "tasks",
  "tournaments",
  "audit",
]);

const TOURNAMENT_VIEWS: ReadonlySet<string> = new Set([
  "overview",
  "participants",
  "bracket",
  "conduct",
  "audit",
]);

const asSearchParams = (search: string | URLSearchParams): URLSearchParams =>
  typeof search === "string" ? new URLSearchParams(search) : search;

export const parseAdminNavigation = (
  search: string | URLSearchParams,
): AdminNavigation => {
  const params = asSearchParams(search);
  const rawSection = params.get("section") || DEFAULT_ADMIN_NAVIGATION.section;
  const section: AdminSection = ADMIN_SECTIONS.has(rawSection)
    ? (rawSection as AdminSection)
    : DEFAULT_ADMIN_NAVIGATION.section;
  const rawView = params.get("view") || DEFAULT_ADMIN_NAVIGATION.view;
  const view: TournamentAdminView =
    section === "audit"
      ? "audit"
      : section === "tournaments" && TOURNAMENT_VIEWS.has(rawView)
        ? (rawView as TournamentAdminView)
        : "overview";
  const tournament = params.get("tournament")?.trim() || "";

  return {
    section,
    tournamentId: tournament || null,
    view,
  };
};

export const adminNavigationEquals = (
  left: AdminNavigation,
  right: AdminNavigation,
): boolean =>
  left.section === right.section &&
  left.tournamentId === right.tournamentId &&
  left.view === right.view;

export const adminNavigationSearch = (
  navigation: AdminNavigation,
): string => {
  const params = new URLSearchParams();
  params.set("section", navigation.section);
  if (navigation.tournamentId) {
    params.set("tournament", navigation.tournamentId);
  }
  params.set("view", navigation.view);
  return params.toString();
};
