import type { components } from "../../shared/api/schema";

/** Raw Arena tournament payload from the generated REST contract. */
export type Tournament = components["schemas"]["Tournament"];
export type PublicTournament = components["schemas"]["PublicTournamentResponse"];
export type TournamentList = components["schemas"]["TournamentListResponse"];
