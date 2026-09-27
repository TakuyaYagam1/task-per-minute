"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import {
  ApiError,
  createOperatorCommandIntent,
  operatorApi,
  type OperatorRecoverySnapshot,
  type Tournament,
} from "../../shared/api";
import { formatTournamentState } from "../../shared/lib";
import { Button, Message, Panel, Status } from "../../shared/ui";

import styles from "./TournamentStartControls.module.css";

type TournamentStartView = "participants" | "bracket";
type LifecycleAction = "open_registration" | "start_swiss";

type TournamentStartControlsProps = Readonly<{
  onNavigate: (view: TournamentStartView) => void;
  onReloadTournaments?: () => Promise<void>;
  onSessionExpired?: () => void;
  onTournamentUpdated?: (tournament: Tournament) => void;
  tournament: Tournament;
}>;

const expectedStateFor = (action: LifecycleAction): Tournament["state"] =>
  action === "open_registration" ? "draft" : "roster_locked";

const actionLabel = (action: LifecycleAction): string =>
  action === "open_registration" ? "Открыть регистрацию" : "Начать швейцарский этап";

const actionReason = (action: LifecycleAction): string =>
  action === "open_registration"
    ? "Оператор подтвердил открытие регистрации"
    : "Оператор подтвердил запуск швейцарского этапа";

const isAbortError = (error: unknown): boolean =>
  error instanceof DOMException &&
  (error.name === "AbortError" || error.name === "TimeoutError");

const problemMessage = (error: unknown, fallback: string): string => {
  if (error instanceof ApiError) {
    return error.problem?.detail || error.problem?.title || fallback;
  }
  return fallback;
};

const stateLabel = (state: Tournament["state"]): string =>
  formatTournamentState(state);

const stateTone = (state: Tournament["state"]): "neutral" | "info" | "live" | "success" | "error" => {
  switch (state) {
    case "draft":
      return "neutral";
    case "registration":
    case "roster_locked":
      return "info";
    case "swiss":
    case "golden":
    case "playoffs":
    case "technical_pause":
      return "live";
    case "completed":
      return "success";
    case "cancelled":
      return "error";
    default:
      return "neutral";
  }
};

const canStartAction = (
  action: LifecycleAction,
  state: Tournament["state"],
): boolean => state === expectedStateFor(action);

const isProjectionRevision = (value: number): boolean =>
  Number.isSafeInteger(value) && value > 0;

const transitionConflictMessage =
  "Не удалось выполнить переход. Обновите данные и проверьте готовность состава.";
const activeTournamentConflictDetail = "another tournament is already active";
const activeTournamentConflictMessage =
  "Уже идет другой турнир. Завершите или отмените его перед запуском нового.";

export const TournamentStartControls = ({
  onNavigate,
  onReloadTournaments,
  onSessionExpired,
  onTournamentUpdated,
  tournament,
}: TournamentStartControlsProps) => {
  const [busyAction, setBusyAction] = useState<LifecycleAction | null>(null);
  const [commandError, setCommandError] = useState<string | null>(null);
  const [stale, setStale] = useState(false);
  const [actionUnavailable, setActionUnavailable] = useState(false);
  const [successAction, setSuccessAction] = useState<LifecycleAction | null>(null);
  const commandInFlightRef = useRef(false);
  const commandControllerRef = useRef<AbortController | null>(null);
  const generationRef = useRef(0);
  const mountedRef = useRef(false);

  useEffect(() => {
    mountedRef.current = true;
    generationRef.current += 1;
    setBusyAction(null);
    setCommandError(null);
    setStale(false);
    setActionUnavailable(false);
    setSuccessAction(null);

    return () => {
      mountedRef.current = false;
      generationRef.current += 1;
      commandControllerRef.current?.abort();
      commandControllerRef.current = null;
      commandInFlightRef.current = false;
    };
  }, [tournament.id]);

  const refreshTournaments = useCallback((): void => {
    setCommandError(null);
    setStale(false);
    setActionUnavailable(false);
    void onReloadTournaments?.();
  }, [onReloadTournaments]);

  const handleLifecycleAction = useCallback(
    async (action: LifecycleAction): Promise<void> => {
      if (
        commandInFlightRef.current ||
        stale ||
        !canStartAction(action, tournament.state) ||
        !mountedRef.current
      ) {
        return;
      }

      commandInFlightRef.current = true;
      const generation = generationRef.current;
      const controller = new AbortController();
      commandControllerRef.current = controller;
      const canApply = (): boolean =>
        mountedRef.current &&
        generationRef.current === generation &&
        !controller.signal.aborted;

      setBusyAction(action);
      setCommandError(null);
      setStale(false);
      setActionUnavailable(false);
      setSuccessAction(null);

      try {
        const snapshot: OperatorRecoverySnapshot = await operatorApi.getSnapshot(
          tournament.id,
          undefined,
          controller.signal,
        );
        if (!canApply()) {
          return;
        }

        if (snapshot.tournament.id !== tournament.id) {
          setActionUnavailable(false);
          setStale(true);
          setCommandError("Данные относятся к другому турниру. Обновите данные перед повтором.");
          return;
        }

        if (!canStartAction(action, snapshot.tournament.state)) {
          onTournamentUpdated?.(snapshot.tournament);
          setActionUnavailable(false);
          setStale(true);
          setCommandError(
            `Состояние турнира уже изменилось: ${stateLabel(snapshot.tournament.state)}. Обновите данные перед повтором.`,
          );
          return;
        }

        const projectionRevision = snapshot.next_cursor.projection_revision;
        if (!isProjectionRevision(projectionRevision)) {
          setActionUnavailable(false);
          setStale(true);
          setCommandError("Не удалось получить актуальные данные для перехода. Обновите данные перед повтором.");
          return;
        }

        const updatedTournament = await operatorApi.applyTournamentAction(
          tournament.id,
          {
            action,
            confirmed: true,
            expected_projection_revision: projectionRevision,
            reason: actionReason(action),
          },
          createOperatorCommandIntent(),
          controller.signal,
        );
        if (!canApply()) {
          return;
        }

        onTournamentUpdated?.(updatedTournament);
        setSuccessAction(action);
      } catch (error) {
        if (!canApply() || isAbortError(error)) {
          return;
        }
        if (error instanceof ApiError && error.status === 401) {
          onSessionExpired?.();
        }
        if (error instanceof ApiError && error.status === 409) {
          setStale(true);
          setActionUnavailable(true);
          setCommandError(
            error.problem?.detail === activeTournamentConflictDetail
              ? activeTournamentConflictMessage
              : transitionConflictMessage,
          );
        } else if (error instanceof ApiError && error.status === 422) {
          setActionUnavailable(false);
          setCommandError(
            problemMessage(error, "Переход пока недоступен. Проверьте состав и обновите данные."),
          );
        } else {
          setActionUnavailable(false);
          setCommandError(problemMessage(error, "Не удалось изменить этап турнира"));
        }
      } finally {
        if (commandControllerRef.current === controller) {
          commandControllerRef.current = null;
        }
        if (canApply()) {
          commandInFlightRef.current = false;
          setBusyAction(null);
        }
      }
    },
    [onSessionExpired, onTournamentUpdated, stale, tournament],
  );

  const action =
    tournament.state === "draft"
      ? "open_registration"
      : tournament.state === "roster_locked"
        ? "start_swiss"
        : null;

  return (
    <Panel
      className={styles.root}
      data-testid="tournament-start-controls"
      title="Запуск турнира"
      description="Откройте регистрацию и запустите турнир после фиксации состава."
    >
      <div className={styles.toolbar}>
        <Status tone={stateTone(tournament.state)}>
          {stateLabel(tournament.state)}
        </Status>
        {onReloadTournaments ? (
          <Button
            size="small"
            variant="secondary"
            onClick={refreshTournaments}
          >
            Обновить данные
          </Button>
        ) : null}
      </div>

      {commandError ? (
        <Message
          tone="error"
          title={actionUnavailable ? "Действие недоступно" : stale ? "Состояние изменилось" : "Действие не выполнено"}
        >
          {commandError}
          {onReloadTournaments ? (
            <button className={styles.inlineAction} type="button" onClick={refreshTournaments}>
              Обновить данные
            </button>
          ) : null}
        </Message>
      ) : null}

      {successAction ? (
        <Message tone="success" title="Переход выполнен">
          {successAction === "open_registration"
            ? "Регистрация открыта. После набора участников проверьте состав в разделе \"Участники\"."
            : "Швейцарский этап запущен. Откройте сетку и серии, чтобы проверить пары."}
          {successAction === "open_registration" ? (
            <Button
              className={styles.followUpButton}
              size="small"
              variant="secondary"
              onClick={() => onNavigate("participants")}
            >
              Открыть участников
            </Button>
          ) : null}
          {successAction === "start_swiss" ? (
            <Button
              className={styles.followUpButton}
              size="small"
              variant="secondary"
              onClick={() => onNavigate("bracket")}
            >
              Открыть сетку и серии
            </Button>
          ) : null}
        </Message>
      ) : null}

      {action === "open_registration" ? (
        <Message tone="info" title="Регистрация закрыта">
          Откройте регистрацию, затем добавьте участников и подтвердите состав в разделе &quot;Участники&quot;.
        </Message>
      ) : null}
      {tournament.state === "registration" ? (
        <Message tone="info" title="Подтвердите состав">
          Проверьте участников и зафиксируйте состав в разделе &quot;Участники&quot;, чтобы открыть запуск швейцарского этапа.
        </Message>
      ) : null}
      {action === "start_swiss" ? (
        <Message tone="info" title="Состав зафиксирован">
          Проверьте зафиксированный состав и запустите швейцарский этап.
        </Message>
      ) : null}

      {action ? (
        <div className={styles.actionRow}>
          <Button
            size="large"
            loading={busyAction === action}
            loadingLabel={action === "open_registration" ? "Открываем регистрацию" : "Запускаем швейцарский этап"}
            disabled={stale}
            onClick={() => void handleLifecycleAction(action)}
          >
            {actionLabel(action)}
          </Button>
        </div>
      ) : null}
    </Panel>
  );
};

TournamentStartControls.displayName = "TournamentStartControls";
