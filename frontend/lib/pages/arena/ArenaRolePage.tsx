"use client";

import { useCallback, useEffect, useState } from "react";

import { playerModel } from "../../entities/player";
import {
  buildParticipantPlayerView,
  type ParticipantDraftIntent,
  type ParticipantDraftResult,
  type ParticipantReadyIntent,
  type ParticipantReadyResult,
  type ParticipantSubmissionIntent,
  type ParticipantSubmissionResult,
  type ParticipantSurrenderIntent,
  type ParticipantSurrenderResult,
} from "../../features/tournament-player";
import { TournamentRecoveryPanel } from "../../features/tournament-live";
import { TournamentOperatorActions } from "../../features/tournament-operator-actions";
import {
  adminApi,
  ApiError,
  clearAdminSession,
  createParticipantCommandIntent,
  operatorApi,
  participantApi,
  publicTournamentApi,
  type OperatorRecoverySnapshot,
  type PublicTournamentResponse,
} from "../../shared/api";
import { formatTournamentState } from "../../shared/lib";
import {
  ArenaShell,
  buildArenaLoginHref,
  type ArenaAccessMessage,
  type ArenaAccessStatus,
  type ArenaRole,
  type ArenaRoleSummary,
} from "../../widgets/arena";
import { TournamentPlayerPanel } from "../../widgets/tournament-player";

type ArenaRolePageProps = Readonly<{
  role: ArenaRole;
  tournamentId: string;
}>;

type ArenaRoleState = Readonly<{
  accessStatus: ArenaAccessStatus;
  tournament?: PublicTournamentResponse;
  tournamentName?: string;
}>;

const ROLE_LABELS: Readonly<Record<ArenaRole, string>> = {
  participant: "Участник",
  operator: "Оператор",
  spectator: "Наблюдатель",
};

const initialState: ArenaRoleState = { accessStatus: "loading" };

const isAbortError = (error: unknown): boolean =>
  error instanceof DOMException &&
  (error.name === "AbortError" || error.name === "TimeoutError");

const accessStatusForError = (
  error: unknown,
  tournamentExists: boolean,
): ArenaAccessStatus => {
  if (error instanceof ApiError) {
    if (error.status === 401) {
      return "unauthorized";
    }
    if (error.status === 403) {
      return "forbidden";
    }
    if (error.status === 404) {
      return tournamentExists ? "forbidden" : "missing";
    }
    if (error.status === 0 || error.kind === "transport") {
      return "transport";
    }
  }
  return "transport";
};

const tournamentStateLabel = (state: PublicTournamentResponse["state"]): string =>
  formatTournamentState(state);

const summaryFor = (
  tournament: PublicTournamentResponse,
  role: ArenaRole,
  tournamentName?: string,
): ArenaRoleSummary => ({
  title: `${tournamentName?.trim() || "Турнир"} - ${ROLE_LABELS[role]}: ${tournamentStateLabel(tournament.state)}`,
  description:
    role === "spectator"
      ? "Публичный контур турнира доступен для просмотра."
      : "Доступ подтвержден. В этом контуре пока доступен только статус турнира.",
  metrics: [
    { label: "Состояние", value: tournamentStateLabel(tournament.state) },
    { label: "Размер состава", value: String(tournament.roster_size) },
    { label: "Ревизия", value: String(tournament.projection_revision) },
  ],
  readOnly: tournament.state === "completed",
});

const messageFor = (
  status: ArenaAccessStatus,
  role: ArenaRole,
  tournamentId: string,
): ArenaAccessMessage | undefined => {
  switch (status) {
    case "loading":
      return {
        tone: "loading",
        title: "Проверяем доступ",
        description: "Подключаемся к турниру и проверяем текущий контур.",
      };
    case "unauthorized":
      return {
        tone: "warning",
        title: "Требуется вход",
        description:
          role === "participant"
            ? "Войдите как участник, чтобы открыть этот контур турнира."
            : "Войдите как оператор, чтобы открыть этот контур турнира.",
        action: {
          href: buildArenaLoginHref(
            role === "participant" ? "participant" : "operator",
            tournamentId,
          ),
          label: role === "participant" ? "Войти как участник" : "Войти как оператор",
        },
      };
    case "forbidden":
      return {
        tone: "error",
        title: "Доступ запрещен",
        description:
          "Сессия распознана, но этой роли не разрешен доступ к выбранному турниру.",
      };
    case "missing":
      return {
        tone: "error",
        title: "Турнир не найден",
        description: "Проверьте идентификатор турнира или выберите другой контур.",
        action: { href: "/arena", label: "Выбрать другой турнир" },
      };
    case "transport":
      return {
        tone: "error",
        title: "Не удалось подключиться",
        description: "Сервис турниров временно недоступен. Попробуйте обновить страницу.",
        action: {
          href: `/arena/${role}/${encodeURIComponent(tournamentId)}`,
          label: "Повторить",
        },
      };
    case "completed":
      return {
        tone: "info",
        title: "Турнир завершен",
        description: "Итоговое состояние зафиксировано. Доступен только просмотр.",
      };
    case "ready":
      return undefined;
    default:
      return undefined;
  }
};

export const ArenaRolePage = ({ role, tournamentId }: ArenaRolePageProps) => {
  const [state, setState] = useState<ArenaRoleState>(initialState);
  const [logoutPending, setLogoutPending] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;

    setState(initialState);

    const load = async (): Promise<void> => {
      let tournamentExists = false;
      try {
        const tournament = await publicTournamentApi.getPublicTournament(
          tournamentId,
          controller.signal,
        );
        tournamentExists = true;

        if (cancelled) {
          return;
        }

        let tournamentName: string | undefined;

        if (role === "participant") {
          await participantApi.getLobby(tournamentId, controller.signal);
        } else if (role === "operator") {
          const snapshot: OperatorRecoverySnapshot = await operatorApi.getSnapshot(
            tournamentId,
            undefined,
            controller.signal,
          );
          tournamentName = snapshot.tournament.name;
        }

        if (cancelled) {
          return;
        }

        if (tournament.state === "completed") {
          setState({
            accessStatus: "completed",
            tournament,
            tournamentName,
          });
          return;
        }

        if (!cancelled) {
          setState({ accessStatus: "ready", tournament, tournamentName });
        }
      } catch (error) {
        if (cancelled || isAbortError(error)) {
          return;
        }
        setState({
          accessStatus: accessStatusForError(error, tournamentExists),
        });
      }
    };

    void load();

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [role, tournamentId]);

  const handleLogout = useCallback(async () => {
    if (
      logoutPending ||
      (state.accessStatus !== "ready" && state.accessStatus !== "completed")
    ) {
      return;
    }

    setLogoutPending(true);
    try {
      if (role === "participant") {
        await playerModel.clearCurrentPlayer();
      } else if (role === "operator") {
        try {
          await adminApi.logout();
        } finally {
          clearAdminSession();
        }
      }
    } catch {
      // Local session cleanup still makes this route unauthorized if the
      // remote logout request is unavailable.
    } finally {
      setState({ accessStatus: "unauthorized" });
      setLogoutPending(false);
    }
  }, [logoutPending, role, state.accessStatus]);

  const handleParticipantReady = useCallback(async (
    intent: ParticipantReadyIntent,
  ): Promise<ParticipantReadyResult> => {
    const waveId = intent.key.waveId;
    if (
      waveId === null ||
      intent.key.tournamentId !== tournamentId
    ) {
      return {
        message: "Назначение изменилось. Обновите состояние турнира.",
        status: "conflict",
      };
    }

    const result = await participantApi.ready(
      tournamentId,
      waveId,
      {
        expected_projection_revision: intent.key.projectionRevision,
        ready: intent.ready,
      },
      createParticipantCommandIntent(),
    );

    if (result.status === "success") {
      return { status: "accepted" };
    }

    if (result.status === "rate_limited") {
      return {
        message: result.retryAfter
          ? `Повторите после ${result.retryAfter}.`
          : undefined,
        status: "rate_limited",
      };
    }

    return {
      message: "Окно готовности или назначение уже изменились. Сверяем данные с сервером.",
      status: "conflict",
    };
  }, [tournamentId]);

  const handleParticipantSubmit = useCallback(async (
    intent: ParticipantSubmissionIntent,
  ): Promise<ParticipantSubmissionResult> => {
    if (
      intent.tournamentId !== tournamentId ||
      intent.submittedFlag.trim().length === 0
    ) {
      return {
        message: "Ответ не соответствует текущему назначению.",
        status: "conflict",
      };
    }

    const result = await participantApi.submitFlag(
      tournamentId,
      intent.seriesId,
      intent.gameId,
      {
        expected_projection_revision: intent.projectionRevision,
        submitted_flag: intent.submittedFlag,
      },
      createParticipantCommandIntent(),
    );

    if (result.status === "success") {
      return {
        status: result.value.submission.correct ? "accepted" : "incorrect",
      };
    }

    if (result.status === "rate_limited") {
      return {
        message: result.retryAfter
          ? `Повторите после ${result.retryAfter}.`
          : undefined,
        status: "rate_limited",
      };
    }

    return {
      message: "Состояние игры изменилось. Сверяем данные с сервером.",
      status: "conflict",
    };
  }, [tournamentId]);

  const handleParticipantSurrender = useCallback(async (
    intent: ParticipantSurrenderIntent,
  ): Promise<ParticipantSurrenderResult> => {
    if (intent.tournamentId !== tournamentId) {
      return {
        message: "Сдача не относится к текущему турниру.",
        status: "conflict",
      };
    }

    const result = await participantApi.surrender(
      tournamentId,
      intent.seriesId,
      {
        confirmed: true,
        expected_projection_revision: intent.projectionRevision,
      },
      createParticipantCommandIntent(),
    );

    if (result.status === "success") {
      return { status: "accepted" };
    }

    if (result.status === "rate_limited") {
      return {
        message: result.retryAfter
          ? `Повторите после ${result.retryAfter}.`
          : undefined,
        status: "rate_limited",
      };
    }

    return {
      message: "Состояние игры изменилось. Сверяем данные с сервером.",
      status: "conflict",
    };
  }, [tournamentId]);

  const handleParticipantDraft = useCallback(async (
    intent: ParticipantDraftIntent,
  ): Promise<ParticipantDraftResult> => {
    if (intent.tournamentId !== tournamentId || intent.action !== "ban") {
      return {
        message: "Ход не относится к текущему турниру.",
        status: "conflict",
      };
    }

    const result = await participantApi.draftAction(
      tournamentId,
      intent.seriesId,
      {
        action: intent.action,
        category: intent.category,
        expected_draft_revision: intent.draftRevision,
        expected_projection_revision: intent.projectionRevision,
        expected_turn: intent.expectedTurn,
      },
      createParticipantCommandIntent(),
    );

    if (result.status === "success") {
      return { status: "accepted" };
    }

    if (result.status === "rate_limited") {
      return {
        message: result.retryAfter
          ? `Повторите после ${result.retryAfter}.`
          : undefined,
        status: "rate_limited",
      };
    }

    return {
      message: "Драфт уже изменился. Сверяем данные с сервером.",
      status: "conflict",
    };
  }, [tournamentId]);

  const summary = state.tournament
    ? summaryFor(state.tournament, role, state.tournamentName)
    : undefined;
  const accessMessage = messageFor(state.accessStatus, role, tournamentId);

  return (
    <ArenaShell
      role={role}
      tournamentId={tournamentId}
      accessStatus={state.accessStatus}
      accessMessage={accessMessage}
      summary={summary}
      logoutPending={logoutPending}
      onLogout={
        role !== "spectator" &&
        (state.accessStatus === "ready" || state.accessStatus === "completed")
          ? handleLogout
          : undefined
      }
    >
      {(state.accessStatus === "ready" || state.accessStatus === "completed") && (
        role === "participant" ? (
          <TournamentRecoveryPanel role={role} tournamentId={tournamentId}>
            {({ recovery, retry }) => (
              <TournamentPlayerPanel
                onDraft={async (intent) => {
                  const result = await handleParticipantDraft(intent);
                  if (result.status !== "rate_limited") {
                    retry();
                  }
                  return result;
                }}
                onReady={async (intent) => {
                  const result = await handleParticipantReady(intent);
                  if (result.status !== "rate_limited") {
                    retry();
                  }
                  return result;
                }}
                onSubmit={async (intent) => {
                  const result = await handleParticipantSubmit(intent);
                  if (result.status !== "rate_limited") {
                    retry();
                  }
                  return result;
                }}
                onSurrender={async (intent) => {
                  const result = await handleParticipantSurrender(intent);
                  if (result.status !== "rate_limited") {
                    retry();
                  }
                  return result;
                }}
                view={buildParticipantPlayerView(recovery)}
              />
            )}
          </TournamentRecoveryPanel>
        ) : (
          <TournamentRecoveryPanel role={role} tournamentId={tournamentId} />
        )
      )}
      {role === "operator" && state.accessStatus === "ready" && (
        <TournamentOperatorActions tournamentId={tournamentId} />
      )}
    </ArenaShell>
  );
};

ArenaRolePage.displayName = "ArenaRolePage";
