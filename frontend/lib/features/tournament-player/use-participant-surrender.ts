"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import type { ParticipantPlayerView } from "./model";

export type ParticipantSurrenderStatus =
  | "idle"
  | "confirming"
  | "pending"
  | "accepted"
  | "conflict"
  | "rate_limited"
  | "error";

export type ParticipantSurrenderIntent = Readonly<{
  projectionRevision: number;
  seriesId: string;
  tournamentId: string;
}>;

export type ParticipantSurrenderResult = Readonly<{
  message?: string;
  status: "accepted" | "conflict" | "rate_limited";
}>;

type ParticipantSurrenderView = Readonly<{
  allowed: boolean;
  cancel: () => void;
  confirm: () => void;
  message: string | null;
  requestConfirmation: () => void;
  status: ParticipantSurrenderStatus;
}>;

type ParticipantSurrenderState = Readonly<{
  message: string | null;
  status: ParticipantSurrenderStatus;
}>;

type UseParticipantSurrenderOptions = Readonly<{
  onSurrender: (intent: ParticipantSurrenderIntent) => Promise<ParticipantSurrenderResult>;
  view: ParticipantPlayerView | null;
}>;

const initialState: ParticipantSurrenderState = {
  message: null,
  status: "idle",
};

const surrenderAllowed = (view: ParticipantPlayerView | null): boolean => {
  const assignment = view?.assignment;
  return (
    view !== null &&
    assignment !== null &&
    assignment !== undefined &&
    view.assignmentDeliveryState === "delivered" &&
    (view.state === "assigned" || view.state === "ready") &&
    (assignment.gameState === "active" || assignment.gameState === "paused")
  );
};

const defaultMessageFor = (status: ParticipantSurrenderResult["status"]): string => {
  switch (status) {
    case "accepted":
      return "Сдача принята. Официальный итог обновится после проверки.";
    case "conflict":
      return "Данные игры изменились. Обновляем матч.";
    case "rate_limited":
      return "Слишком много попыток. Повторите после паузы.";
  }
};

/**
 * Exposes surrender only for the current participant assignment. The server
 * remains the authority for identity, allowed state, and the official result.
 */
export const useParticipantSurrender = ({
  onSurrender,
  view,
}: UseParticipantSurrenderOptions): ParticipantSurrenderView => {
  const assignment = view?.assignment ?? null;
  const assignmentToken = assignment === null || view === null
    ? "none"
    : `${view.tournamentId}:${assignment.seriesId}:${view.projectionRevision}`;
  const [state, setState] = useState(initialState);
  const inFlightRef = useRef(false);
  const requestRef = useRef(0);

  useEffect(() => {
    requestRef.current += 1;
    inFlightRef.current = false;
    setState(initialState);
  }, [assignmentToken]);

  const requestConfirmation = useCallback(() => {
    if (surrenderAllowed(view) && !inFlightRef.current) {
      setState({ message: null, status: "confirming" });
    }
  }, [view]);

  const cancel = useCallback(() => {
    if (state.status === "confirming") {
      setState(initialState);
    }
  }, [state.status]);

  const confirm = useCallback(() => {
    if (
      state.status !== "confirming" ||
      view === null ||
      !surrenderAllowed(view) ||
      assignment === null ||
      inFlightRef.current
    ) {
      return;
    }

    inFlightRef.current = true;
    const requestId = ++requestRef.current;
    const intent: ParticipantSurrenderIntent = {
      projectionRevision: view.projectionRevision,
      seriesId: assignment.seriesId,
      tournamentId: view.tournamentId,
    };
    setState({ message: "Отправляем сдачу.", status: "pending" });

    void onSurrender(intent)
      .then((result) => {
        if (requestId !== requestRef.current) {
          return;
        }
        setState({
          message: result.message ?? defaultMessageFor(result.status),
          status: result.status,
        });
      })
      .catch(() => {
        if (requestId !== requestRef.current) {
          return;
        }
        setState({
          message: "Не удалось отправить сдачу. Повторите попытку.",
          status: "error",
        });
      })
      .finally(() => {
        if (requestId === requestRef.current) {
          inFlightRef.current = false;
        }
      });
  }, [assignment, onSurrender, state.status, view]);

  return { ...state, allowed: surrenderAllowed(view), cancel, confirm, requestConfirmation };
};
