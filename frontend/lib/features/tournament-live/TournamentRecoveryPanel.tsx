"use client";

import type { ReactNode } from "react";

import type { RoleAwareRecoveryState, TournamentLiveRole } from "../../shared/api";
import { useServerCountdown } from "./use-server-countdown";
import { useOperatorTournamentRealtime } from "./use-operator-tournament-realtime";
import { useParticipantTournamentRealtime } from "./use-participant-tournament-realtime";
import { usePublicTournamentRealtime } from "./use-public-tournament-realtime";
import { useTournamentRecovery } from "./use-tournament-recovery";
import { TournamentLivePanel, type TournamentLiveConnectionStatus } from "./TournamentLivePanel";
import styles from "./TournamentLivePanel.module.css";

type ArenaLiveRole = TournamentLiveRole | "spectator";

export type TournamentRecoveryRenderContext = Readonly<{
  participantRefreshSequence: number;
  recovery: RoleAwareRecoveryState | null;
  receivedAtMonotonicMs?: number;
  retry: () => void;
  status: TournamentLiveConnectionStatus;
}>;

type TournamentRecoveryPanelProps = Readonly<{
  role: ArenaLiveRole;
  tournamentId: string;
  children?: (context: TournamentRecoveryRenderContext) => ReactNode;
}>;

const recoveryRole = (role: ArenaLiveRole): TournamentLiveRole =>
  role === "spectator" ? "public" : role;

const deadlineFrom = (state: RoleAwareRecoveryState): string | undefined => {
  const snapshot = state.snapshot;
  if (state.role === "participant" && "lobby" in snapshot) {
    const deadlinesSuppressed = snapshot.runtime?.pause?.deadlines_suppressed === true ||
      snapshot.wave?.state === "paused" ||
      snapshot.series?.state === "technical_pause" ||
      snapshot.assignment?.context.game_state === "paused" ||
      snapshot.draft?.state === "paused";
    if (deadlinesSuppressed) {
      return undefined;
    }
    if (snapshot.assignment !== null) {
      return snapshot.assignment.context.effective_deadline ?? undefined;
    }
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

const operatorPanelStatus = (
  status: TournamentLiveConnectionStatus,
  realtimeStatus: ReturnType<typeof useOperatorTournamentRealtime>["status"],
): TournamentLiveConnectionStatus => {
  switch (realtimeStatus) {
    case "connecting":
    case "reconnecting":
      return "recovering";
    case "connected":
    case "paused":
      return "live";
    case "error":
      return "stale";
    case "rejected":
      return "rejected";
    case "idle":
      return status;
  }
};

const publicPanelStatus = (
  status: TournamentLiveConnectionStatus,
  realtimeStatus: ReturnType<typeof usePublicTournamentRealtime>["status"],
): TournamentLiveConnectionStatus => {
  switch (realtimeStatus) {
    case "connecting":
    case "reconnecting":
    case "recovering":
      return "recovering";
    case "connected":
      return "live";
    case "error":
      return "stale";
    case "rejected":
      return "rejected";
    case "idle":
      return status;
  }
};

const participantPanelStatus = (
  status: TournamentLiveConnectionStatus,
  realtimeStatus: ReturnType<typeof useParticipantTournamentRealtime>["status"],
): TournamentLiveConnectionStatus => {
  switch (realtimeStatus) {
    case "connecting":
    case "reconnecting":
    case "recovering":
      return "recovering";
    case "connected":
    case "idle":
      return status;
    case "error":
      return "stale";
    case "rejected":
      return "rejected";
  }
};

const readyMemberCount = (state: ReturnType<typeof useOperatorTournamentRealtime>["state"]): number =>
  state?.operator.waves.reduce((total, wave) => {
    const members = Array.isArray(wave.members) ? wave.members : [];
    return total + members.filter((member) => (
      typeof member === "object" && member !== null && member.ready === true
    )).length;
  }, 0) ?? 0;

const operatorConnectionLabel = (
  status: ReturnType<typeof useOperatorTournamentRealtime>["status"],
): string => {
  switch (status) {
    case "connecting":
    case "reconnecting":
      return "Восстанавливаем";
    case "connected":
      return "На связи";
    case "paused":
      return "Пауза";
    case "rejected":
      return "Доступ отклонен";
    case "error":
      return "Данные устарели";
    case "idle":
      return "Ожидание";
  }
};

const publicConnectionLabel = (
  status: ReturnType<typeof usePublicTournamentRealtime>["status"],
): string => {
  switch (status) {
    case "connecting":
    case "reconnecting":
      return "Восстанавливаем";
    case "recovering":
      return "Синхронизируем";
    case "connected":
      return "На связи";
    case "rejected":
      return "Доступ отклонен";
    case "error":
      return "Данные устарели";
    case "idle":
      return "Ожидание";
  }
};

type CountdownPanelProps = Readonly<{
  deadline: string;
  children?: ReactNode;
  receivedAtMonotonicMs?: number;
  recovery: RoleAwareRecoveryState;
  revision?: number;
  retry: () => void;
  status: TournamentLiveConnectionStatus;
}>;

const CountdownPanel = ({
  deadline,
  children,
  receivedAtMonotonicMs,
  recovery,
  revision,
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
      revision={revision ?? recovery.cursor.projection_revision}
      countdown={countdown}
      onRetry={retry}
    >
      {children}
    </TournamentLivePanel>
  );
};

export const TournamentRecoveryPanel = ({
  children,
  role,
  tournamentId,
}: TournamentRecoveryPanelProps) => {
  const liveRole = recoveryRole(role);
  const { receivedAtMonotonicMs, recovery, retry, status } = useTournamentRecovery(
    liveRole,
    tournamentId,
  );
  const operatorRealtime = useOperatorTournamentRealtime({
    enabled: liveRole === "operator",
    recovery: liveRole === "operator" ? recovery : null,
    recoveryReceivedAtMonotonicMs: liveRole === "operator"
      ? receivedAtMonotonicMs
      : undefined,
    tournamentId,
  });
  const publicRealtime = usePublicTournamentRealtime({
    enabled: liveRole === "public",
    recovery: liveRole === "public" ? recovery : null,
    retry,
    tournamentId,
  });
  const participantRealtime = useParticipantTournamentRealtime({
    enabled: liveRole === "participant",
    recovery: liveRole === "participant" ? recovery : null,
    retry,
    tournamentId,
  });
  const panelStatus = liveRole === "operator"
    ? operatorPanelStatus(status, operatorRealtime.status)
    : liveRole === "public"
      ? publicPanelStatus(status, publicRealtime.status)
      : participantPanelStatus(status, participantRealtime.status);
  const retryAll = () => {
    if (liveRole === "participant") {
      participantRealtime.retry();
    }
    retry();
  };
  const panelRevision = operatorRealtime.state?.projectionRevision ??
    publicRealtime.state?.cursor.projection_revision ??
    recovery?.cursor.projection_revision;
  const operatorState = liveRole === "operator" ? (
    <dl
      aria-label="Состояние realtime оператора"
      className={styles.telemetry}
      data-connection={operatorRealtime.status}
      data-paused={operatorRealtime.paused ? "true" : "false"}
      data-projection-revision={operatorRealtime.state?.projectionRevision ?? ""}
      data-ready={operatorRealtime.ready ? "true" : "false"}
      data-testid="operator-realtime-summary"
    >
      <div>
        <dt>Соединение</dt>
        <dd>{operatorConnectionLabel(operatorRealtime.status)}</dd>
      </div>
      <div>
        <dt>Готовы</dt>
        <dd>{readyMemberCount(operatorRealtime.state)}</dd>
      </div>
      <div>
        <dt>На связи</dt>
        <dd>{operatorRealtime.state?.operator.presence.length ?? 0}</dd>
      </div>
      <div>
        <dt>Пауза</dt>
        <dd>{operatorRealtime.paused ? "Да" : "Нет"}</dd>
      </div>
      <div>
        <dt>Ревизия</dt>
        <dd>{operatorRealtime.state?.projectionRevision ?? recovery?.cursor.projection_revision ?? "-"}</dd>
      </div>
    </dl>
  ) : null;
  const publicState = liveRole === "public" ? (
    <dl
      aria-label="Состояние realtime публичного просмотра"
      className={styles.telemetry}
      data-connection={publicRealtime.status}
      data-projection-revision={publicRealtime.state?.cursor.projection_revision ?? ""}
      data-ready={publicRealtime.ready ? "true" : "false"}
      data-testid="public-realtime-summary"
    >
      <div>
        <dt>Соединение</dt>
        <dd>{publicConnectionLabel(publicRealtime.status)}</dd>
      </div>
      <div>
        <dt>Участники</dt>
        <dd>{publicRealtime.state?.display.scoreboard.length ?? 0}</dd>
      </div>
      <div>
        <dt>Результаты</dt>
        <dd>{publicRealtime.state?.display.officialResults.length ?? 0}</dd>
      </div>
      <div>
        <dt>Ревизия</dt>
        <dd>{publicRealtime.state?.cursor.projection_revision ?? recovery?.cursor.projection_revision ?? "-"}</dd>
      </div>
    </dl>
  ) : null;
  const realtimeState = operatorState ?? publicState;
  const deadline = recovery ? deadlineFrom(recovery) : undefined;
  const roleSlot = liveRole === "participant" && children !== undefined
      ? children({
        participantRefreshSequence: participantRealtime.refreshSequence,
        receivedAtMonotonicMs,
        recovery,
        retry,
        status: panelStatus,
      })
    : null;

  if (recovery && deadline) {
    return (
      <>
        {roleSlot}
        <CountdownPanel
          deadline={deadline}
          receivedAtMonotonicMs={receivedAtMonotonicMs}
          recovery={recovery}
          revision={panelRevision}
          retry={retryAll}
          status={panelStatus}
        >
          {realtimeState}
        </CountdownPanel>
      </>
    );
  }

  return (
    <>
      {roleSlot}
      <TournamentLivePanel
        role={liveRole}
        status={panelStatus}
        tournamentId={tournamentId}
        revision={panelRevision}
        onRetry={recovery || panelStatus === "rejected" ? retryAll : undefined}
      >
        {realtimeState}
      </TournamentLivePanel>
    </>
  );
};
