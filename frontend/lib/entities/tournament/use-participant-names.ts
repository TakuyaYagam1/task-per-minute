"use client";

import { useCallback, useEffect, useMemo, useState } from "react";

import { adminApi, operatorApi, type AdminPlayer, type Roster } from "../../shared/api";
import { ADMIN_PLAYERS_CHANGED_EVENT } from "../../shared/api/admin";

type MatchParticipants = Readonly<{ first_participant_id: string; second_participant_id: string }>;
type Names = Readonly<{ tournamentId: string; players: readonly AdminPlayer[]; roster: Roster | null }>;

// Names are presentation data. Commands continue to use the IDs in the current snapshot.
export const useParticipantNames = (tournamentId: string, roster?: Roster | null) => {
  const [names, setNames] = useState<Names | null>(null);
  const fetchRoster = roster === undefined;

  useEffect(() => {
    if (!tournamentId) return;
    let controller: AbortController | null = null;
    const load = () => {
      controller?.abort();
      const request = new AbortController();
      controller = request;
      void Promise.all([
        adminApi.listPlayers(true, request.signal),
        fetchRoster ? operatorApi.getRoster(tournamentId, request.signal) : Promise.resolve(null),
      ]).then(([players, loadedRoster]) => {
        if (!request.signal.aborted) setNames({ tournamentId, players, roster: loadedRoster });
      }).catch(() => {
        if (!request.signal.aborted) setNames(null);
      });
    };
    load();
    window.addEventListener(ADMIN_PLAYERS_CHANGED_EVENT, load);
    return () => {
      controller?.abort();
      window.removeEventListener(ADMIN_PLAYERS_CHANGED_EVENT, load);
    };
  }, [fetchRoster, tournamentId]);

  const currentNames = names?.tournamentId === tournamentId ? names : null;
  const currentRoster = roster === undefined ? currentNames?.roster : roster;
  const participantNames = useMemo(() => {
    const players = new Map(currentNames?.players.map((player) => [
      player.id,
      `${player.username}${player.deleted_at ? " (удален)" : ""}`,
    ]));
    return new Map(
      (currentRoster?.tournament_id === tournamentId ? currentRoster.participants : []).map((participant) => [
        participant.id,
        players.get(participant.player_id) || `Участник ${participant.seed}`,
      ]),
    );
  }, [currentNames, currentRoster, tournamentId]);

  const participantName = useCallback((id: string) => participantNames.get(id) ?? "Имя недоступно", [participantNames]);
  const matchName = useCallback((match: MatchParticipants) =>
    `${participantName(match.first_participant_id)} / ${participantName(match.second_participant_id)}`,
  [participantName]);

  return { participantName, matchName };
};
