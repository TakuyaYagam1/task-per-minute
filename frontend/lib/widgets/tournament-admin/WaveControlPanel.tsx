"use client";

import { useCallback, useMemo, useRef, useState } from "react";
import { useParticipantNames } from "../../entities/tournament";

import {
  ApiError,
  createOperatorCommandIntent,
  operatorApi,
  type OperatorRecoverySnapshot,
  type OperatorRecoveryCursor,
  type RoleAwareRecoveryState,
  type Wave,
  type WaveControlRequest,
} from "../../shared/api";
import { useOperatorTournamentRealtime } from "../../features/tournament-live/use-operator-tournament-realtime";
import { useTournamentRecovery } from "../../features/tournament-live/use-tournament-recovery";
import { useServerCountdown } from "../../features/tournament-live/use-server-countdown";
import { formatCountdown } from "../../features/tournament-live/countdown";
import {
  CATEGORY_LABELS,
  WAVE_STATE_LABELS,
  formatSeriesState,
  formatReadyWindowState,
} from "../../shared/lib";
import { Button, Message, Panel, Status } from "../../shared/ui";

import styles from "./WaveControlPanel.module.css";

type WaveControlPanelProps = Readonly<{
  onSessionExpired?: () => void;
  tournamentId: string;
}>;

type WaveAction = Extract<WaveControlRequest["action"], "open_ready_window" | "start">;

type PresenceView = Readonly<{
  participantId: string;
  seriesId: string;
  state: "connected" | "disconnected";
}>;

type OperatorRecoveryState = Omit<RoleAwareRecoveryState, "cursor" | "role" | "snapshot"> & {
  cursor: OperatorRecoveryCursor;
  role: "operator";
  snapshot: OperatorRecoverySnapshot;
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === "object" && !Array.isArray(value);

const realtimeWaveStates = new Set<Wave["state"]>([
  "planned",
  "ready_window_open",
  "ready",
  "active",
  "paused",
  "completed",
  "ready_window_expired",
  "superseded",
]);

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

const isUUID = (value: unknown): value is string =>
  typeof value === "string" && UUID_PATTERN.test(value);

const isDateTime = (value: unknown): value is string =>
  typeof value === "string" && Number.isFinite(Date.parse(value));

const hasOnlyKeys = (
  value: Record<string, unknown>,
  required: readonly string[],
  optional: readonly string[] = [],
): boolean => {
  const allowed = new Set([...required, ...optional]);
  return required.every((key) => key in value) && Object.keys(value).every((key) => allowed.has(key));
};

const readPresence = (value: unknown): PresenceView | null => {
  if (!isRecord(value)) {
    return null;
  }
  const participantId = value.participant_id;
  const seriesId = value.series_id;
  const state = value.state;
  if (
    typeof participantId !== "string" ||
    typeof seriesId !== "string" ||
    (state !== "connected" && state !== "disconnected")
  ) {
    return null;
  }
  return { participantId, seriesId, state };
};

const operatorRecoveryFrom = (
  recovery: RoleAwareRecoveryState | null,
): OperatorRecoveryState | null => {
  if (recovery?.role !== "operator") {
    return null;
  }
  return recovery as OperatorRecoveryState;
};

type RealtimeWaveMember = Readonly<{
  participantId: string;
  ready: boolean;
  readinessRevision: number;
}>;

type RealtimeWaveProjection = Readonly<{
  members: readonly RealtimeWaveMember[];
  state: Wave["state"];
  waveId: string;
  windowDeadline?: string;
}>;

const parseRealtimeWave = (value: unknown): RealtimeWaveProjection | null => {
  if (!isRecord(value) || !hasOnlyKeys(value, ["wave_id", "state", "members"], ["window_deadline"])) {
    return null;
  }
  if (!isUUID(value.wave_id) || typeof value.state !== "string" || !realtimeWaveStates.has(value.state as Wave["state"])) {
    return null;
  }
  if (!Array.isArray(value.members) || value.members.length < 2) {
    return null;
  }
  const members: RealtimeWaveMember[] = [];
  for (const candidate of value.members) {
    if (!isRecord(candidate) || !hasOnlyKeys(candidate, ["participant_id", "ready", "readiness_revision"], ["series_id"])) {
      return null;
    }
    const readinessRevision = candidate.readiness_revision;
    if (
      !isUUID(candidate.participant_id) ||
      typeof candidate.ready !== "boolean" ||
      typeof readinessRevision !== "number" ||
      !Number.isSafeInteger(readinessRevision) ||
      readinessRevision < 1
    ) {
      return null;
    }
    if (candidate.series_id !== undefined && candidate.series_id !== null && !isUUID(candidate.series_id)) {
      return null;
    }
    members.push({
      participantId: candidate.participant_id,
      ready: candidate.ready,
      readinessRevision,
    });
  }
  if (value.window_deadline !== undefined && !isDateTime(value.window_deadline)) {
    return null;
  }
  return {
    members,
    state: value.state as Wave["state"],
    waveId: value.wave_id,
    ...(value.window_deadline === undefined ? {} : { windowDeadline: value.window_deadline }),
  };
};

const mergeRealtimeWave = (wave: Wave, projection: RealtimeWaveProjection): Wave => {
  const membersByParticipant = new Map(
    projection.members.map((member) => [member.participantId, member] as const),
  );
  const readyWindow = wave.ready_window && projection.windowDeadline
    ? { ...wave.ready_window, deadline: projection.windowDeadline }
    : wave.ready_window;
  return {
    ...wave,
    members: wave.members.map((member) => {
      const liveMember = membersByParticipant.get(member.participant_id);
      return liveMember
        ? { ...member, ready: liveMember.ready, readiness_revision: liveMember.readinessRevision }
        : member;
    }),
    ready_window: readyWindow,
    state: projection.state,
  };
};

const waveActionReason = (action: WaveAction): string =>
  action === "open_ready_window"
    ? "Оператор открыл окно готовности"
    : "Оператор запустил волну после подтверждения готовности";

const problemMessage = (error: unknown, fallback: string): string => {
  if (error instanceof ApiError) {
    return error.problem?.detail || error.problem?.title || fallback;
  }
  return fallback;
};

const connectionLabel = (state: PresenceView["state"] | undefined): string => {
  if (state === "connected") {
    return "На связи";
  }
  if (state === "disconnected") {
    return "Связь потеряна";
  }
  return "Нет данных";
};

const connectionTone = (
  state: PresenceView["state"] | undefined,
): "success" | "error" | "neutral" => {
  if (state === "connected") {
    return "success";
  }
  if (state === "disconnected") {
    return "error";
  }
  return "neutral";
};

const seriesForWave = (
  wave: Wave,
  seriesById: ReadonlyMap<string, OperatorRecoverySnapshot["series"][number]>,
): readonly OperatorRecoverySnapshot["series"][number][] => {
  const seriesIds = new Set<string>();
  for (const member of wave.members) {
    if (member.series_id) {
      seriesIds.add(member.series_id);
    }
  }
  return [...seriesIds]
    .map((seriesId) => seriesById.get(seriesId))
    .filter((series): series is OperatorRecoverySnapshot["series"][number] => series !== undefined);
};

const membersForSeries = (
  wave: Wave,
  series: OperatorRecoverySnapshot["series"][number],
) => {
  const participantIds = new Set([
    series.first_participant_id,
    series.second_participant_id,
  ]);
  return wave.members.filter((member) => (
    member.series_id === series.id || participantIds.has(member.participant_id)
  ));
};

const WaveCountdown = ({
  deadline,
  receivedAtMonotonicMs,
  recovery,
}: Readonly<{
  deadline: string;
  receivedAtMonotonicMs?: number;
  recovery: RoleAwareRecoveryState;
}>) => {
  const countdown = useServerCountdown({
    deadline,
    receivedAtMonotonicMs,
    serverTimestamp: recovery.serverTimestamp,
  });
  return (
    <span
      className={styles.countdown}
      data-countdown-status={countdown.status}
      data-testid="wave-ready-countdown"
    >
      {formatCountdown(countdown.remainingMs)}
    </span>
  );
};

type WaveCardProps = Readonly<{
  busyAction: string | null;
  connectionByParticipant: ReadonlyMap<string, PresenceView["state"]>;
  onAction: (wave: Wave, action: WaveAction) => void;
  recovery: RoleAwareRecoveryState;
  receivedAtMonotonicMs?: number;
  seriesById: ReadonlyMap<string, OperatorRecoverySnapshot["series"][number]>;
  stale: boolean;
  wave: Wave;
  waveNumber: number;
  participantName: (id: string) => string;
  matchName: (series: OperatorRecoverySnapshot["series"][number]) => string;
}>;

const WaveCard = ({
  busyAction,
  connectionByParticipant,
  onAction,
  recovery,
  receivedAtMonotonicMs,
  seriesById,
  stale,
  wave,
  waveNumber,
  participantName,
  matchName,
}: WaveCardProps) => {
  const seriesList = seriesForWave(wave, seriesById);
  const allReady = wave.members.length > 0 && wave.members.every((member) => member.ready);
  const canOpen = wave.state === "planned";
  const canStart = wave.state === "ready" && allReady && wave.ready_window?.state === "open";
  const readyWindow = wave.ready_window;

  return (
    <article className={styles.waveCard} data-testid="operator-wave" data-wave-id={wave.id}>
      <div className={styles.waveHeader}>
        <div>
          <h3>Волна {waveNumber}</h3>
        </div>
        <Status
          tone={wave.state === "active" ? "live" : wave.state === "completed" ? "success" : "neutral"}
        >
          {WAVE_STATE_LABELS[wave.state] ?? wave.state}
        </Status>
      </div>

      <div className={styles.waveMeta}>
        <span>Матчей: {seriesList.length}</span>
      </div>

      <div className={styles.matches}>
        {seriesList.map((series) => (
          <article className={styles.match} data-testid="operator-match" data-series-id={series.id} key={series.id}>
            <div className={styles.matchHeading}>
              <strong>{matchName(series)}</strong>
              <Status tone={series.state === "active" ? "live" : "neutral"}>
                {formatSeriesState(series.state)}
              </Status>
            </div>
            <div className={styles.matchMeta}>
              <span>Окно готовности</span>
              {readyWindow?.state === "open" ? (
                <WaveCountdown
                  deadline={readyWindow.deadline}
                  receivedAtMonotonicMs={receivedAtMonotonicMs}
                  recovery={recovery}
                />
              ) : (
                <span>{readyWindow ? formatReadyWindowState(readyWindow.state) : "Не открыто"}</span>
              )}
            </div>
            <div className={styles.members} data-testid="operator-match-members">
              {membersForSeries(wave, series).map((member) => {
                const presence = connectionByParticipant.get(member.participant_id);
                return (
                  <div className={styles.member} key={`${series.id}-${member.participant_id}`}>
                    <div className={styles.memberIdentity}>
                      <span>Участник</span>
                      <strong>{participantName(member.participant_id)}</strong>
                    </div>
                    <div className={styles.memberSignals}>
                      <Status tone={member.ready ? "success" : "warning"}>
                        {member.ready ? "Готов" : "Не готов"}
                      </Status>
                      <Status tone={connectionTone(presence)}>{connectionLabel(presence)}</Status>
                    </div>
                  </div>
                );
              })}
            </div>
            <div className={styles.score} aria-label={`Счет матча: ${matchName(series)}`}>
              <span>{series.score.first_participant_wins}</span>
              <small>:</small>
              <span>{series.score.second_participant_wins}</span>
            </div>
            <div className={styles.categories}>
              {series.slots.map((slot) => (
                <span className={styles.category} key={slot.id}>
                  {slot.position}. {CATEGORY_LABELS[slot.category] ?? slot.category}
                </span>
              ))}
            </div>
          </article>
        ))}
        {seriesList.length === 0 ? (
          <p className={styles.muted}>Для этой волны пока нет матчей.</p>
        ) : null}
      </div>

      <div className={styles.actions}>
        <Button
          size="small"
          variant="secondary"
          data-testid={`wave-open-${wave.id}`}
          disabled={stale || !canOpen || busyAction !== null}
          loading={busyAction === `${wave.id}:open_ready_window`}
          loadingLabel="Открываем"
          onClick={() => onAction(wave, "open_ready_window")}
        >
          Открыть готовность
        </Button>
        <Button
          size="small"
          variant="success"
          data-testid={`wave-start-${wave.id}`}
          disabled={stale || !canStart || busyAction !== null}
          loading={busyAction === `${wave.id}:start`}
          loadingLabel="Запускаем"
          onClick={() => onAction(wave, "start")}
        >
          Начать волну
        </Button>
      </div>
    </article>
  );
};

export const WaveControlPanel = ({
  onSessionExpired,
  tournamentId,
}: WaveControlPanelProps) => {
  const [commandError, setCommandError] = useState<string | null>(null);
  const [stale, setStale] = useState(false);
  const [busyAction, setBusyAction] = useState<string | null>(null);
  const commandInFlightRef = useRef(false);
  const { receivedAtMonotonicMs, recovery, refresh, status } = useTournamentRecovery(
    "operator",
    tournamentId,
  );
  const realtime = useOperatorTournamentRealtime({
    enabled: true,
    recovery: recovery?.role === "operator" ? recovery : null,
    recoveryReceivedAtMonotonicMs: receivedAtMonotonicMs,
    tournamentId,
  });
  const operatorRecovery = operatorRecoveryFrom(recovery);
  const snapshot = operatorRecovery?.snapshot ?? null;
  const { participantName, matchName } = useParticipantNames(tournamentId, snapshot?.roster ?? null);

  const realtimeWaves = useMemo(() => {
    const waves = realtime.state?.operator.waves ?? [];
    const result = new Map<string, RealtimeWaveProjection>();
    for (const candidate of waves) {
      const projection = parseRealtimeWave(candidate);
      if (projection) {
        result.set(projection.waveId, projection);
      }
    }
    return result;
  }, [realtime.state]);

  const waves = useMemo(
    () => snapshot?.waves.map((wave) => {
      const projection = realtimeWaves.get(wave.id);
      return projection ? mergeRealtimeWave(wave, projection) : wave;
    }) ?? [],
    [realtimeWaves, snapshot],
  );
  const seriesById = useMemo(
    () => new Map((snapshot?.series ?? []).map((series) => [series.id, series] as const)),
    [snapshot],
  );
  const connectionByParticipant = useMemo(() => {
    const presence = new Map<string, PresenceView["state"]>();
    const fallbackPresence = snapshot?.pause_graph?.presence ?? [];
    for (const entry of fallbackPresence) {
      const parsed = readPresence(entry);
      if (parsed) {
        presence.set(parsed.participantId, parsed.state);
      }
    }
    for (const entry of realtime.state?.operator.presence ?? []) {
      const parsed = readPresence(entry);
      if (parsed) {
        presence.set(parsed.participantId, parsed.state);
      }
    }
    return presence;
  }, [realtime.state, snapshot]);

  const representedSeriesIds = useMemo(
    () => new Set(waves.flatMap((wave) => wave.members.flatMap((member) => (
      member.series_id ? [member.series_id] : []
    )))),
    [waves],
  );
  const unassignedSeries = useMemo(
    () => (snapshot?.series ?? []).filter((series) => !representedSeriesIds.has(series.id)),
    [representedSeriesIds, snapshot],
  );
  const nextActionableWave = waves.find((wave) => (
    wave.state === "planned" ||
    wave.state === "ready_window_open" ||
    wave.state === "ready" ||
    wave.state === "active"
  ));
  const nextExpiredWave = [...waves].reverse().find((wave) => wave.state === "ready_window_expired");
  const nextWave = nextActionableWave ?? nextExpiredWave;
  const nextWaveAllReady = nextWave !== undefined && nextWave.members.length > 0 && nextWave.members.every((member) => member.ready);
  const readinessGuidance = nextWave === undefined
    ? null
    : nextWave.state === "planned"
      ? "Откройте готовность, чтобы участники могли подтвердить участие."
      : nextWave.state === "ready_window_open"
        ? nextWaveAllReady
          ? "Все участники подтвердили готовность. Дождитесь перехода волны к запуску."
          : "Ожидаем подтверждения участников. Запуск станет доступен после полной готовности пары."
        : nextWave.state === "ready_window_expired"
          ? "Окно готовности истекло. Обновите данные и проверьте участников, которые не подтвердили готовность."
          : nextWave.state === "active"
            ? "Волна запущена. Следите за матчами и готовьте следующий раунд после их завершения."
            : nextWaveAllReady
              ? "Все участники готовы. Можно запускать волну."
              : "Ожидаем подтверждения участников. Запуск станет доступен после полной готовности пары.";

  const handleAction = useCallback(async (wave: Wave, action: WaveAction): Promise<void> => {
    if (commandInFlightRef.current || operatorRecovery === null) {
      return;
    }
    const key = `${wave.id}:${action}`;
    commandInFlightRef.current = true;
    setBusyAction(key);
    setCommandError(null);
    setStale(false);
    try {
      const projectionRevision = Math.max(
        operatorRecovery.cursor.projection_revision,
        realtime.state?.projectionRevision ?? 0,
      );
      const body: WaveControlRequest = {
        action,
        confirmed: true,
        expected_projection_revision: projectionRevision,
        reason: waveActionReason(action),
      };
      await operatorApi.controlWave(
        tournamentId,
        wave.id,
        body,
        createOperatorCommandIntent(),
      );
      refresh();
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      if (error instanceof ApiError && error.status === 409) {
        setStale(true);
        setCommandError("Состояние соревнования устарело. Обновите данные перед повтором.");
      } else {
        setCommandError(problemMessage(error, "Не удалось изменить состояние волны"));
      }
    } finally {
      commandInFlightRef.current = false;
      setBusyAction(null);
    }
  }, [onSessionExpired, operatorRecovery, realtime.state, refresh, tournamentId]);

  if (!tournamentId) {
    return null;
  }

  return (
    <Panel
      title="Волны и матчи"
      description="Следите за готовностью игроков, запускайте матчи и просматривайте счет."
      className={styles.root}
      data-testid="operator-wave-control-panel"
    >
      <div className={styles.toolbar}>
        <div>
          <strong>Соревнование</strong>
          <strong>{snapshot?.tournament.name || "Загружаем соревнование"}</strong>
        </div>
        <div className={styles.connection} data-testid="operator-wave-connection">
          <Status tone={realtime.status === "connected" ? "success" : "warning"}>
            {realtime.status === "connected" ? "Обновляется автоматически" : "Автообновление недоступно"}
          </Status>
          <Button
            size="small"
            variant="secondary"
            onClick={() => {
              setStale(false);
              setCommandError(null);
              refresh();
            }}
          >
            Обновить матчи
          </Button>
        </div>
      </div>

      {status === "connecting" && !snapshot ? (
        <Message tone="loading" title="Загружаем состояние">
          Получаем актуальные данные соревнования.
        </Message>
      ) : null}
      {status === "stale" && !snapshot ? (
        <Message tone="error" title="Данные соревнования недоступны">
          <button className={styles.linkButton} type="button" onClick={refresh}>Обновить данные</button>
        </Message>
      ) : null}
      {commandError ? (
        <Message tone="error" title={stale ? "Состояние изменилось" : "Действие не выполнено"}>
          {commandError}
          <button className={styles.linkButton} type="button" onClick={() => { setStale(false); setCommandError(null); refresh(); }}>
            Обновить данные
          </button>
        </Message>
      ) : null}

      {operatorRecovery ? (
        <>
          {readinessGuidance ? (
            <Message tone="info" title="Следующий шаг">
              {readinessGuidance}
            </Message>
          ) : null}
          <div className={styles.board} data-testid="operator-wave-board">
            {waves.map((wave, index) => (
              <WaveCard
                busyAction={busyAction}
                connectionByParticipant={connectionByParticipant}
                key={wave.id}
                onAction={(currentWave, action) => void handleAction(currentWave, action)}
                recovery={operatorRecovery}
                receivedAtMonotonicMs={receivedAtMonotonicMs}
                seriesById={seriesById}
                stale={stale}
                wave={wave}
                waveNumber={index + 1}
                participantName={participantName}
                matchName={matchName}
              />
            ))}
          </div>
          {unassignedSeries.length > 0 ? (
            <div className={styles.unassigned} data-testid="operator-unassigned-matches">
              <h3>Матчи без активной волны</h3>
              {unassignedSeries.map((series) => (
                <div className={styles.unassignedRow} key={series.id}>
                  <strong>{matchName(series)}</strong>
                  <span>{series.score.first_participant_wins}:{series.score.second_participant_wins}</span>
                  <span>{series.slots.map((slot) => CATEGORY_LABELS[slot.category] ?? slot.category).join(", ")}</span>
                </div>
              ))}
            </div>
          ) : null}
        </>
      ) : null}

      {operatorRecovery && waves.length === 0 ? (
        <Message tone="empty" title="Волн пока нет">
          В этом соревновании пока нет активных или завершенных волн.
        </Message>
      ) : null}
    </Panel>
  );
};

WaveControlPanel.displayName = "WaveControlPanel";
