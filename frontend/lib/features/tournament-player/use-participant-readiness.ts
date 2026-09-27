"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import type {
  ParticipantPlayerView,
  ParticipantReadyIntent,
  ParticipantReadyResult,
  ParticipantReadinessKey,
} from "./model";

type ReadinessStatus = "idle" | "submitting" | "accepted" | "error";

type ReadinessView = Readonly<{
  status: ReadinessStatus;
  ready: boolean;
  message: string | null;
}>;

type ReadinessActions = Readonly<{
  submitReady: () => void;
}>;

type UseParticipantReadinessOptions = Readonly<{
  view: ParticipantPlayerView | null;
  onReady: (intent: ParticipantReadyIntent) => Promise<ParticipantReadyResult>;
}>;

const initialView: ReadinessView = {
  message: null,
  ready: false,
  status: "idle",
};

const keyToken = (key: ParticipantReadinessKey): string => [
  key.tournamentId,
  key.projectionRevision,
  key.waveId ?? "none",
  key.readyWindowId ?? "none",
  key.readyWindowRevisionId ?? "none",
  key.assignmentAttemptId ?? "none",
].join(":");

const defaultMessageFor = (status: ParticipantReadyResult["status"]): string => {
  switch (status) {
    case "accepted":
      return "Готовность подтверждена.";
    case "conflict":
      return "Данные изменились. Обновляем матч.";
    case "rate_limited":
      return "Слишком много попыток. Повторите после паузы.";
  }
};

/**
 * Owns only the local readiness intent. Recovery data stays server-owned.
 * A readiness key change invalidates pending work from the previous window.
 */
export const useParticipantReadiness = ({
  onReady,
  view,
}: UseParticipantReadinessOptions): ReadinessView & ReadinessActions => {
  const currentKey = view?.readyKey ?? null;
  const currentToken = currentKey === null ? "none" : keyToken(currentKey);
  const [state, setState] = useState<ReadinessView>(() => ({
    ...initialView,
    ready: view?.ready === true,
  }));
  const tokenRef = useRef(currentToken);
  const requestRef = useRef(0);

  useEffect(() => {
    if (tokenRef.current === currentToken) {
      return;
    }

    tokenRef.current = currentToken;
    requestRef.current += 1;
    setState({
      ...initialView,
      ready: view?.ready === true,
    });
  }, [currentToken, view?.ready]);

  useEffect(() => {
    if (currentKey === null || view?.ready !== true) {
      return;
    }
    setState((previous) => ({
      ...previous,
      ready: view?.ready === true || previous.ready,
      status: previous.status === "submitting" ? previous.status : "accepted",
    }));
  }, [currentKey, view?.ready]);

  const submitReady = useCallback(() => {
    if (
      currentKey === null ||
      !view?.readyWindowOpen ||
      view.state === "bye" ||
      view.state === "completed" ||
      view.state === "eliminated" ||
      state.status === "submitting" ||
      state.ready
    ) {
      return;
    }

    const request = ++requestRef.current;
    const requestToken = currentToken;
    const intent: ParticipantReadyIntent = { key: currentKey, ready: true };
    setState({ message: null, ready: false, status: "submitting" });

    void onReady(intent)
      .then((result) => {
        if (request !== requestRef.current || requestToken !== tokenRef.current) {
          return;
        }
        setState({
          message: result.message ?? defaultMessageFor(result.status),
          ready: result.status === "accepted",
          status: result.status === "accepted" ? "accepted" : "error",
        });
      })
      .catch(() => {
        if (request !== requestRef.current || requestToken !== tokenRef.current) {
          return;
        }
        setState({
          message: "Не удалось подтвердить готовность. Повторите попытку.",
          ready: false,
          status: "error",
        });
      });
  }, [currentKey, currentToken, onReady, state.ready, state.status, view]);

  return { ...state, submitReady };
};
