"use client";

import type { RoleAwareRecoveryState, TournamentLiveRole } from "../../shared/api";
import { useServerCountdown } from "./use-server-countdown";
import { useTournamentRecovery } from "./use-tournament-recovery";
import { TournamentLivePanel, type TournamentLiveConnectionStatus } from "./TournamentLivePanel";

type ArenaLiveRole = TournamentLiveRole | "spectator";

type TournamentRecoveryPanelProps = Readonly<{
  role: ArenaLiveRole;
  tournamentId: string;
}>;

const recoveryRole = (role: ArenaLiveRole): TournamentLiveRole =>
  role === "spectator" ? "public" : role;

const deadlineFrom = (state: RoleAwareRecoveryState): string | undefined => {
  const snapshot = state.snapshot;
  if (state.role === "participant" && "lobby" in snapshot) {
    const draftDeadline = snapshot.draft?.state === "active"
      ? snapshot.draft.turn_deadline
      : undefined;
    const readyDeadline = snapshot.wave?.ready_window?.state === "open"
      ? snapshot.wave.ready_window.deadline
      : undefined;
    return draftDeadline ?? readyDeadline ?? undefined;
  }
  if (state.role === "operator" && "pause_graph" in snapshot) {
    if (snapshot.pause_graph?.deadlines_suppressed) {
      return undefined;
    }
    const pausedDraft = snapshot.pause_graph?.draft;
    const pausedWindow = snapshot.pause_graph?.wave.ready_window;
    const pausedDeadline = (pausedDraft?.state === "active" ? pausedDraft.turn_deadline : null) ??
      (pausedWindow?.state === "open" ? pausedWindow.deadline : null);
    if (pausedDeadline) {
      return pausedDeadline;
    }
    return snapshot.waves.find((wave) => wave.ready_window?.state === "open")
      ?.ready_window?.deadline;
  }
  return undefined;
};

type CountdownPanelProps = Readonly<{
  deadline: string;
  receivedAtMonotonicMs?: number;
  recovery: RoleAwareRecoveryState;
  retry: () => void;
  status: TournamentLiveConnectionStatus;
}>;

const CountdownPanel = ({
  deadline,
  receivedAtMonotonicMs,
  recovery,
  retry,
  status,
}: CountdownPanelProps) => {
  const countdown = useServerCountdown({
    deadline,
    receivedAtMonotonicMs,
    serverTimestamp: recovery.serverTimestamp,
  });
  return (
    <TournamentLivePanel
      role={recovery.role}
      status={status}
      tournamentId={recovery.tournamentId}
      revision={recovery.cursor.projection_revision}
      countdown={countdown}
      onRetry={retry}
    />
  );
};

export const TournamentRecoveryPanel = ({
  role,
  tournamentId,
}: TournamentRecoveryPanelProps) => {
  const liveRole = recoveryRole(role);
  const { receivedAtMonotonicMs, recovery, retry, status } = useTournamentRecovery(
    liveRole,
    tournamentId,
  );
  const deadline = recovery ? deadlineFrom(recovery) : undefined;

  if (recovery && deadline) {
    return (
      <CountdownPanel
        deadline={deadline}
        receivedAtMonotonicMs={receivedAtMonotonicMs}
        recovery={recovery}
        retry={retry}
        status={status}
      />
    );
  }

  return (
    <TournamentLivePanel
      role={liveRole}
      status={status}
      tournamentId={tournamentId}
      revision={recovery?.cursor.projection_revision}
      onRetry={recovery || status === "rejected" ? retry : undefined}
    />
  );
};
