"use client";

import Link from "next/link";
import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react";

import { playerModel } from "../../entities/player";
import type { Player } from "../../shared/types";
import { ApiError } from "../../shared/api/client";
import { ApiContractError } from "../../shared/api/guards";
import {
  getTournamentAdmissionStatus,
  joinTournamentAdmission,
  cancelTournamentAdmission,
  createTournamentAdmissionCommandIntent,
  type TournamentAdmissionConflictReason,
  type TournamentAdmissionMutationResult,
  type TournamentAdmissionStatus,
  type TournamentAdmissionView,
} from "../../shared/api/tournament-admission";
import { participantApi } from "../../shared/api/participant";
import type { components } from "../../shared/api/schema";
import {
  buildArenaPublicTournamentPath,
  getSafeArenaPublicReturnPath,
  isValidUsername,
  formatTournamentState,
} from "../../shared/lib";
import { Button, Message, Panel, Status, type MessageTone, type StatusTone } from "../../shared/ui";

import styles from "./TournamentEntry.module.css";

type TournamentState = components["schemas"]["TournamentState"];

const getSafeArenaCatalogReturnPath = (value: string | null | undefined): string | null => {
  const safePath = getSafeArenaPublicReturnPath(value);
  if (safePath === "/arena" || safePath?.startsWith("/arena?")) {
    return safePath;
  }
  return null;
};

export type TournamentEntryProps = Readonly<{
  catalogReturnPath?: string | null;
  publicId: string;
  returnPath?: string | null;
  state: TournamentState;
  tournamentId: string;
}>;

type BusyAction = "join" | "cancel" | "refresh" | "workspace" | null;
type Phase = "idle" | "loading" | "ready" | "error";

type Notice = Readonly<{
  body: string;
  title: string;
  tone: MessageTone;
}>;

const EXECUTION_STATES: ReadonlySet<TournamentState> = new Set([
  "swiss",
  "golden",
  "playoffs",
  "technical_pause",
]);

const TERMINAL_STATES: ReadonlySet<TournamentState> = new Set([
  "completed",
  "cancelled",
]);

const statusLabels: Record<TournamentAdmissionStatus, string> = {
  not_registered: "Регистрация не оформлена",
  invited: "Приглашение ожидает ответа",
  registered: "Ожидает подтверждения",
  checked_in: "Участие подтверждено",
  withdrawn: "Регистрация отменена",
};

const statusTones: Record<TournamentAdmissionStatus, StatusTone> = {
  not_registered: "neutral",
  invited: "info",
  registered: "success",
  checked_in: "success",
  withdrawn: "disabled",
};

const isAbortError = (error: unknown): boolean =>
  error instanceof DOMException &&
  error.name === "AbortError";

const retryMessage = (retryAfter: string | null | undefined): string => {
  const value = retryAfter?.trim();
  if (!value) {
    return "Слишком много запросов. Повторите позже.";
  }
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds > 0) {
    return `Слишком много запросов. Повторите через ${Math.ceil(seconds)} секунд.`;
  }
  return "Слишком много запросов. Повторите позже.";
};

const conflictNotice = (reason: TournamentAdmissionConflictReason): Notice => {
  switch (reason) {
    case "full":
      return {
        body: "Свободных мест в планируемом составе нет.",
        title: "Состав заполнен",
        tone: "warning",
      };
    case "closed":
      return {
        body: "Соревнование больше не принимает новые регистрации.",
        title: "Регистрация закрыта",
        tone: "warning",
      };
    case "withdrawn":
      return {
        body: "Эта регистрация отменена и требует решения оператора.",
        title: "Регистрация отменена",
        tone: "warning",
      };
    case "conflicting_reservation":
      return {
        body: "Игрок уже зарезервирован в другом активном соревновании.",
        title: "Участие уже зарезервировано",
        tone: "warning",
      };
    case "conflict":
      return {
        body: "Данные соревнования изменились. Обновите страницу и повторите попытку.",
        title: "Данные изменились",
        tone: "warning",
      };
  }
  return {
    body: "Данные соревнования изменились. Обновите страницу и повторите попытку.",
    title: "Данные изменились",
    tone: "warning",
  };
};

const apiNotice = (error: unknown, operation: string): Notice => {
  if (error instanceof ApiError) {
    if (error.status === 401) {
      return {
        body: "Сессия игрока истекла. Введите никнейм еще раз.",
        title: "Нужен вход",
        tone: "warning",
      };
    }
    if (error.status === 403) {
      return {
        body: "У этой сессии нет доступа к действию.",
        title: "Доступ запрещен",
        tone: "error",
      };
    }
    if (error.status === 404) {
      return {
        body: "Соревнование больше не доступно для участия.",
        title: "Соревнование не найдено",
        tone: "error",
      };
    }
    if (error.status === 429) {
      return {
        body: retryMessage(error.retryAfter),
        title: "Запрос ограничен",
        tone: "warning",
      };
    }
  }
  if (error instanceof ApiContractError) {
    return {
      body: "Не удалось загрузить данные соревнования. Обновите страницу.",
      title: "Ошибка данных",
      tone: "error",
    };
  }
  return {
    body: `Не удалось выполнить действие: ${operation}. Повторите попытку.`,
    title: "Ошибка соединения",
    tone: "error",
  };
};

const mutationNotice = (
  result: Exclude<TournamentAdmissionMutationResult, { status: "success" }>,
): Notice => {
  if (result.status === "conflict") {
    return conflictNotice(result.reason);
  }
  if (result.status === "rate_limited") {
    return {
      body: retryMessage(result.retryAfter),
      title: "Запрос ограничен",
      tone: "warning",
    };
  }
  if (result.status === "unauthorized") {
    return apiNotice(result.error, "проверить сессию");
  }
  return apiNotice(result.error, "выполнить действие");
};

const viewMatchesContext = (
  view: TournamentAdmissionView,
  tournamentId: string,
  playerId: string,
): boolean => view.tournament_id === tournamentId && view.player_id === playerId;

const participantWorkspacePath = (
  tournamentId: string,
  returnPath: string,
): string =>
  `/arena/participant/${encodeURIComponent(tournamentId)}?return=${encodeURIComponent(returnPath)}`;

const isExecutionState = (value: TournamentState): boolean => EXECUTION_STATES.has(value);

const isTerminalState = (value: TournamentState): boolean => TERMINAL_STATES.has(value);

const isWorkspacePublicationReady = (
  catalogState: TournamentState,
  admissionState?: TournamentState | null,
): boolean => {
  if (isTerminalState(catalogState) || isTerminalState(admissionState ?? catalogState)) {
    return false;
  }
  return isExecutionState(catalogState) || isExecutionState(admissionState ?? catalogState);
};

const canWithdrawAdmission = (
  view: TournamentAdmissionView,
  catalogState: TournamentState,
): boolean =>
  !view.roster_locked &&
  (
    view.status === "invited" ||
    view.status === "registered" ||
    (
      view.status === "checked_in" &&
      catalogState === "registration" &&
      view.tournament_state === "registration"
    )
  );

export const TournamentEntry = ({
  catalogReturnPath,
  publicId,
  returnPath,
  state,
  tournamentId,
}: TournamentEntryProps) => {
  const titleId = useId();
  const mountedRef = useRef(true);
  const requestVersionRef = useRef(0);
  const actionControllerRef = useRef<AbortController | null>(null);
  const [username, setUsername] = useState("");
  const [nameFormOpen, setNameFormOpen] = useState(false);
  const [player, setPlayer] = useState<Player | null>(null);
  const [view, setView] = useState<TournamentAdmissionView | null>(null);
  const [phase, setPhase] = useState<Phase>("idle");
  const [busyAction, setBusyAction] = useState<BusyAction>(null);
  const [notice, setNotice] = useState<Notice | null>(null);
  const [workspaceReady, setWorkspaceReady] = useState(false);
  const [visibilityTick, setVisibilityTick] = useState(0);
  const busyActionRef = useRef<BusyAction>(null);
  busyActionRef.current = busyAction;

  const safeReturnPath = useMemo(
    () => getSafeArenaPublicReturnPath(returnPath) ?? buildArenaPublicTournamentPath(publicId),
    [publicId, returnPath],
  );
  const safeCatalogReturnPath = useMemo(
    () => getSafeArenaCatalogReturnPath(catalogReturnPath) ?? "/arena",
    [catalogReturnPath],
  );
  const workspaceHref = useMemo(
    () => participantWorkspacePath(tournamentId, safeReturnPath),
    [safeReturnPath, tournamentId],
  );

  const isCurrentRequest = useCallback((version: number, signal: AbortSignal): boolean =>
    mountedRef.current && !signal.aborted && requestVersionRef.current === version,
  []);

  const startRequest = useCallback((action: Exclude<BusyAction, null>): {
    controller: AbortController;
    version: number;
  } => {
    actionControllerRef.current?.abort();
    const controller = new AbortController();
    const version = requestVersionRef.current + 1;
    requestVersionRef.current = version;
    actionControllerRef.current = controller;
    setBusyAction(action);
    return { controller, version };
  }, []);

  const applyView = useCallback((nextView: TournamentAdmissionView, nextPlayer: Player): boolean => {
    if (!viewMatchesContext(nextView, tournamentId, nextPlayer.id)) {
      setPhase("error");
      setView(null);
      setWorkspaceReady(false);
      setNotice({
        body: "Данные матча не совпали с текущим игроком. Обновите страницу.",
        title: "Ошибка данных",
        tone: "error",
      });
      return false;
    }
    setView(nextView);
    const publicationReady = isWorkspacePublicationReady(state, nextView.tournament_state);
    setWorkspaceReady((current) => {
      if (!publicationReady) {
        return false;
      }
      if (nextView.status === "checked_in") {
        return true;
      }
      return nextView.status === "registered" ? current : false;
    });
    setPhase("ready");
    return true;
  }, [state, tournamentId]);

  const restoreStatus = useCallback(async (
    candidate: Player | null,
    refreshSession: boolean,
  ): Promise<void> => {
    if (candidate === null && !refreshSession) {
      return;
    }
    const { controller, version } = startRequest("refresh");
    setPhase("loading");
    setNotice(null);
    let currentPlayer = candidate;
    try {
      if (refreshSession) {
        const refreshed = await playerModel.restoreCurrentPlayer(controller.signal);
        if (!isCurrentRequest(version, controller.signal)) {
          return;
        }
        if (refreshed.kind === "aborted") {
          return;
        }
        if (refreshed.kind === "expired") {
          setPlayer(null);
          setView(null);
          setPhase("idle");
          setWorkspaceReady(false);
          setNameFormOpen(Boolean(candidate));
          if (candidate) {
            setNotice({
              body: "Введите никнейм, чтобы продолжить.",
              title: "Сессия истекла",
              tone: "warning",
            });
          }
          return;
        }
        if (refreshed.kind !== "ok") {
          setPhase("error");
          setView(null);
          setWorkspaceReady(false);
          setNameFormOpen(false);
          setNotice(
            refreshed.kind === "contract"
              ? apiNotice(new ApiContractError("players/me"), "проверить сессию")
              : apiNotice(undefined, "проверить сессию"),
          );
          return;
        }
        currentPlayer = {
          id: refreshed.state.player.id,
          username: refreshed.state.player.username,
        };
        setPlayer(currentPlayer);
        setUsername(currentPlayer.username);
      }

      if (currentPlayer === null) {
        return;
      }

      const nextView = await getTournamentAdmissionStatus(tournamentId, controller.signal);
      if (!isCurrentRequest(version, controller.signal)) {
        return;
      }
      if (applyView(nextView, currentPlayer)) {
        setNotice(null);
      }
    } catch (error) {
      if (!isCurrentRequest(version, controller.signal) || isAbortError(error)) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        setPlayer(null);
        setView(null);
        setPhase("idle");
        setWorkspaceReady(false);
        setNameFormOpen(Boolean(candidate));
        if (candidate) {
          setNotice({
            body: "Введите никнейм, чтобы продолжить.",
            title: "Сессия истекла",
            tone: "warning",
          });
        }
        return;
      }
      setPhase("error");
      setView(null);
      setWorkspaceReady(false);
      setNotice(
        apiNotice(
          error,
          currentPlayer === null ? "проверить сессию" : "загрузить статус участия",
        ),
      );
    } finally {
      if (isCurrentRequest(version, controller.signal)) {
        setBusyAction(null);
      }
    }
  }, [applyView, isCurrentRequest, startRequest, tournamentId]);

  useEffect(() => {
    mountedRef.current = true;
    const candidate = playerModel.getCurrentPlayer();
    setPlayer(candidate);
    if (candidate) {
      setUsername(candidate.username);
    }
    void restoreStatus(candidate, true);
    return () => {
      actionControllerRef.current?.abort();
    };
  }, [restoreStatus]);

  useEffect(() => () => {
    mountedRef.current = false;
    actionControllerRef.current?.abort();
  }, []);

  useEffect(() => {
    const onVisibilityChange = (): void => setVisibilityTick((current) => current + 1);
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => document.removeEventListener("visibilitychange", onVisibilityChange);
  }, []);

  useEffect(() => {
    if (
      player === null ||
      view === null ||
      (view.status !== "registered" && !(view.status === "checked_in" && !workspaceReady)) ||
      busyAction !== null ||
      document.visibilityState !== "visible"
    ) {
      return;
    }

    const timer = window.setTimeout(() => {
      if (document.visibilityState === "visible" && busyActionRef.current === null) {
        void restoreStatus(player, false);
      }
    }, 15000);
    return () => window.clearTimeout(timer);
  }, [busyAction, player, restoreStatus, view, visibilityTick, workspaceReady]);

  const handleMutationResult = useCallback((
    result: TournamentAdmissionMutationResult,
    expectedPlayer: Player,
  ): void => {
    if (result.status === "success") {
      if (applyView(result.value.view, expectedPlayer)) {
        setNotice({
          body: result.value.view.status === "withdrawn"
            ? "Регистрация отменена."
            : "Вы записаны на соревнование.",
          title: result.value.changed ? "Изменения сохранены" : "Участие уже оформлено",
          tone: result.value.view.status === "withdrawn" ? "warning" : "success",
        });
      }
      return;
    }
    setPhase("error");
    setView(null);
    setWorkspaceReady(false);
    setNotice(mutationNotice(result));
  }, [applyView]);

  const joinForPlayer = useCallback(async (
    nextPlayer: Player,
    controller: AbortController,
    version: number,
  ): Promise<void> => {
    try {
      const result = await joinTournamentAdmission(
        tournamentId,
        createTournamentAdmissionCommandIntent(),
        controller.signal,
      );
      if (!isCurrentRequest(version, controller.signal)) {
        return;
      }
      if (result.status === "unauthorized") {
        setPlayer(null);
        setView(null);
        setPhase("idle");
        setNameFormOpen(true);
      }
      handleMutationResult(result, nextPlayer);
    } catch (error) {
      if (!isCurrentRequest(version, controller.signal) || isAbortError(error)) {
        return;
      }
      setPhase("error");
      setView(null);
      setWorkspaceReady(false);
      setNotice(apiNotice(error, "оформить регистрацию"));
    } finally {
      if (isCurrentRequest(version, controller.signal)) {
        setBusyAction(null);
      }
    }
  }, [handleMutationResult, isCurrentRequest, tournamentId]);

  const handleNameSubmit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault();
    const nextUsername = username.trim();
    if (!isValidUsername(nextUsername)) {
      setNotice({
        body: "Используйте 2-50 символов: латиница, цифры, _ или -.",
        title: "Проверьте никнейм",
        tone: "warning",
      });
      return;
    }

    const { controller, version } = startRequest("join");
    setNotice(null);
    try {
      const result = await playerModel.initializePlayer(nextUsername, controller.signal);
      if (!isCurrentRequest(version, controller.signal)) {
        return;
      }
      if (result.kind === "aborted") {
        return;
      }
      if (result.kind === "rate_limited") {
        setPhase("idle");
        setNotice({
          body: retryMessage(result.retryAfter),
          title: "Запрос ограничен",
          tone: "warning",
        });
        return;
      }
      if (result.kind !== "ok") {
        setPhase("error");
        setNotice(apiNotice(undefined, "оформить вход"));
        return;
      }
      setPlayer(result.player);
      setUsername(result.player.username);
      setNameFormOpen(false);
      setPhase("loading");
      await joinForPlayer(result.player, controller, version);
    } finally {
      if (isCurrentRequest(version, controller.signal)) {
        setBusyAction(null);
      }
    }
  };

  const handleJoinExisting = async (): Promise<void> => {
    if (!player || busyAction !== null) {
      return;
    }
    const { controller, version } = startRequest("join");
    setNotice(null);
    await joinForPlayer(player, controller, version);
  };

  const handleRefresh = (): void => {
    if (busyAction !== null) {
      return;
    }
    void restoreStatus(player ?? playerModel.getCurrentPlayer(), true);
  };

  const handleCancel = async (): Promise<void> => {
    if (
      busyAction !== null ||
      !player ||
      !view ||
      !canWithdrawAdmission(view, state)
    ) {
      return;
    }
    const { controller, version } = startRequest("cancel");
    setNotice(null);
    try {
      const result = await cancelTournamentAdmission(
        tournamentId,
        createTournamentAdmissionCommandIntent(),
        controller.signal,
      );
      if (!isCurrentRequest(version, controller.signal)) {
        return;
      }
      if (result.status === "unauthorized") {
        setPlayer(null);
        setView(null);
        setPhase("idle");
        setNameFormOpen(true);
      }
      handleMutationResult(result, player);
    } catch (error) {
      if (!isCurrentRequest(version, controller.signal) || isAbortError(error)) {
        return;
      }
      setPhase("error");
      setNotice(apiNotice(error, "отменить регистрацию"));
    } finally {
      if (isCurrentRequest(version, controller.signal)) {
        setBusyAction(null);
      }
    }
  };

  const handleWorkspaceCheck = async (): Promise<void> => {
    if (
      !player ||
      !view ||
      view.participant_id === null ||
      !isWorkspacePublicationReady(state, view.tournament_state) ||
      busyAction !== null
    ) {
      return;
    }
    const participantId = view.participant_id;
    const { controller, version } = startRequest("workspace");
    setNotice(null);
    try {
      const lobby = await participantApi.getLobby(tournamentId, controller.signal);
      if (!isCurrentRequest(version, controller.signal)) {
        return;
      }
      if (lobby.tournament_id !== tournamentId || lobby.participant_id !== participantId) {
        setWorkspaceReady(false);
        setNotice({
          body: "Не удалось подтвердить ваш матч. Обновите статус и попробуйте снова.",
          title: "Матч пока недоступен",
          tone: "error",
        });
        return;
      }
      setWorkspaceReady(true);
      setNotice({
        body: "Участие подтверждено. Откройте страницу матча, чтобы увидеть следующий шаг.",
        title: "Участие подтверждено",
        tone: "success",
      });
    } catch (error) {
      if (!isCurrentRequest(version, controller.signal) || isAbortError(error)) {
        return;
      }
      setWorkspaceReady(false);
      setNotice(apiNotice(error, "проверить рабочую область"));
    } finally {
      if (isCurrentRequest(version, controller.signal)) {
        setBusyAction(null);
      }
    }
  };

  const renderAnonymous = () => (
    <div className={styles.contentStack}>
      <p className={styles.copy}>
        Наблюдать за соревнованием можно без входа. Для регистрации нужен только никнейм.
      </p>
      {state === "registration" ? (
        nameFormOpen ? (
          <form className={styles.form} onSubmit={handleNameSubmit}>
            <label className={styles.label} htmlFor={`${titleId}-username`}>Никнейм</label>
            <input
              id={`${titleId}-username`}
              className={styles.input}
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              maxLength={50}
              autoComplete="nickname"
              disabled={busyAction !== null}
            />
            <div className={styles.actions}>
              <Button
                type="submit"
                loading={busyAction === "join"}
                loadingLabel="Подключение"
                disabled={busyAction !== null || username.trim().length === 0}
              >
                Подтвердить участие
              </Button>
              <Button
                variant="secondary"
                type="button"
                disabled={busyAction !== null}
                onClick={() => setNameFormOpen(false)}
              >
                Отмена
              </Button>
            </div>
          </form>
        ) : (
          phase === "error" ? (
            <Button
              type="button"
              variant="secondary"
              loading={busyAction === "refresh"}
              disabled={busyAction !== null && busyAction !== "refresh"}
              onClick={handleRefresh}
            >
              Повторить проверку
            </Button>
          ) : (
            <Button type="button" onClick={() => setNameFormOpen(true)}>
              Участвовать
            </Button>
          )
        )
      ) : (
        <div className={styles.contentStack}>
          <Message tone="info" title="Регистрация недоступна">
            Сейчас соревнование: {formatTournamentState(state)}.
          </Message>
          {phase === "error" && (
            <Button
              type="button"
              variant="secondary"
              loading={busyAction === "refresh"}
              disabled={busyAction !== null && busyAction !== "refresh"}
              onClick={handleRefresh}
            >
              Повторить проверку
            </Button>
          )}
        </div>
      )}
    </div>
  );

  const renderView = () => {
    if (!view || !player) {
      return null;
    }
    const workspacePublicationReady = isWorkspacePublicationReady(
      state,
      view.tournament_state,
    );
    const canCancel = canWithdrawAdmission(view, state);
    return (
      <div className={styles.contentStack}>
        <div className={styles.statusLine}>
          <Status
            tone={statusTones[view.status]}
            data-testid="tournament-admission-status"
          >
            {statusLabels[view.status]}
          </Status>
          <span className={styles.playerName}>{player.username}</span>
        </div>
        <p className={styles.copy}>
          Состав: {view.roster_size} из {view.planned_roster_size} мест.
        </p>
        {view.status === "invited" && (
          <p className={styles.copy}>Подтвердите приглашение, чтобы сохранить место.</p>
        )}
        {view.status === "registered" && (
          <p className={styles.copy}>Ожидайте подтверждения участия.</p>
        )}
        {view.status === "checked_in" && (
          <p className={styles.copy}>
            {workspacePublicationReady
              ? "Откройте страницу матча, чтобы увидеть следующий шаг."
              : "Участие подтверждено. Страница матча откроется после старта соревнования."}
          </p>
        )}
        {view.status === "withdrawn" && (
          <p className={styles.copy}>Повторная регистрация доступна только после решения оператора.</p>
        )}
        <div className={styles.actions}>
          {view.status === "not_registered" && state === "registration" && (
            <Button
              type="button"
              loading={busyAction === "join"}
              disabled={busyAction !== null && busyAction !== "join"}
              onClick={handleJoinExisting}
            >
              Участвовать
            </Button>
          )}
          {view.status === "invited" && (
            <Button
              type="button"
              loading={busyAction === "join"}
              disabled={busyAction !== null && busyAction !== "join"}
              onClick={handleJoinExisting}
            >
              Подтвердить участие
            </Button>
          )}
          {view.status === "checked_in" && workspacePublicationReady && workspaceReady && (
            <Link className={styles.primaryLink} href={workspaceHref}>
              Открыть мой матч
            </Link>
          )}
          {view.status === "checked_in" && !workspacePublicationReady && (
            <Status tone="info">Матч откроется после старта соревнования</Status>
          )}
          {view.status === "registered" && workspacePublicationReady && !workspaceReady && (
            <Button
              type="button"
              variant="secondary"
              loading={busyAction === "workspace"}
              disabled={busyAction !== null && busyAction !== "workspace"}
              onClick={handleWorkspaceCheck}
            >
              Проверить матч
            </Button>
          )}
          {view.status === "registered" && workspacePublicationReady && workspaceReady && (
            <Link className={styles.primaryLink} href={workspaceHref}>
              Открыть мой матч
            </Link>
          )}
          {canCancel && (
            <Button
              type="button"
              variant="secondary"
              loading={busyAction === "cancel"}
              disabled={busyAction !== null && busyAction !== "cancel"}
              onClick={handleCancel}
            >
              Отменить регистрацию
            </Button>
          )}
          <Button
            type="button"
            variant="ghost"
            loading={busyAction === "refresh"}
            disabled={busyAction !== null && busyAction !== "refresh"}
            onClick={handleRefresh}
          >
            Обновить статус
          </Button>
        </div>
      </div>
    );
  };

  return (
    <Panel
      as="section"
      className={styles.panel}
      title="Участие в соревновании"
      description="Запишитесь на соревнование и следите за подтверждением участия."
    >
      <div className={styles.entry}>
        {notice && (
          <Message tone={notice.tone} title={notice.title} data-testid="tournament-entry-message">
            {notice.body}
          </Message>
        )}
        {phase === "loading" && (
          <Status tone="loading">Проверяем состояние участия</Status>
        )}
        {phase !== "loading" && view && renderView()}
        {phase !== "loading" && !view && !player && renderAnonymous()}
        {phase !== "loading" && !view && player && (
          <div className={styles.contentStack}>
            <p className={styles.copy}>Статус участия еще не загружен.</p>
            <div className={styles.actions}>
              {state === "registration" && (
                <Button
                  type="button"
                  loading={busyAction === "join"}
                  disabled={busyAction !== null && busyAction !== "join"}
                  onClick={handleJoinExisting}
                >
                  Участвовать
                </Button>
              )}
              <Button
                type="button"
                variant="secondary"
                loading={busyAction === "refresh"}
                disabled={busyAction !== null && busyAction !== "refresh"}
                onClick={handleRefresh}
              >
                Обновить статус
              </Button>
            </div>
          </div>
        )}
        <Link className={styles.returnLink} href={safeCatalogReturnPath}>
          К списку соревнований
        </Link>
      </div>
    </Panel>
  );
};
