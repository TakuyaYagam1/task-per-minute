"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import type { ParticipantPlayerView } from "./model";

export type ParticipantSubmissionStatus =
  | "idle"
  | "empty"
  | "pending"
  | "incorrect"
  | "accepted"
  | "conflict"
  | "rate_limited"
  | "error";

export type ParticipantSubmissionIntent = Readonly<{
  gameId: string;
  projectionRevision: number;
  seriesId: string;
  submittedFlag: string;
  tournamentId: string;
}>;

export type ParticipantSubmissionResult = Readonly<{
  message?: string;
  status: "accepted" | "incorrect" | "conflict" | "rate_limited";
}>;

type ParticipantSubmissionView = Readonly<{
  allowed: boolean;
  message: string | null;
  status: ParticipantSubmissionStatus;
  submit: (value: string) => void;
}>;

type ParticipantSubmissionState = Readonly<{
  message: string | null;
  status: ParticipantSubmissionStatus;
}>;

type UseParticipantSubmissionOptions = Readonly<{
  onSubmit: (intent: ParticipantSubmissionIntent) => Promise<ParticipantSubmissionResult>;
  view: ParticipantPlayerView | null;
}>;

const initialState: ParticipantSubmissionState = {
  message: null,
  status: "idle",
};

const actionAllowed = (view: ParticipantPlayerView | null): boolean => {
  const assignment = view?.assignment;
  return (
    view !== null &&
    assignment !== null &&
    assignment !== undefined &&
    view.assignmentDeliveryState === "delivered" &&
    (view.state === "assigned" || view.state === "ready") &&
    assignment.gameState === "active"
  );
};

const defaultMessageFor = (status: ParticipantSubmissionResult["status"]): string => {
  switch (status) {
    case "accepted":
      return "Ответ принят. Итог появится после официального решения.";
    case "incorrect":
      return "Ответ неверный. Попытка сохранена. Проверьте решение и повторите.";
    case "conflict":
      return "Данные игры изменились. Обновляем матч.";
    case "rate_limited":
      return "Слишком много попыток. Повторите после паузы.";
  }
};

/**
 * Keeps the submitted answer in component memory only. The server owns the
 * correctness decision and any subsequent official result.
 */
export const useParticipantSubmission = ({
  onSubmit,
  view,
}: UseParticipantSubmissionOptions): ParticipantSubmissionView => {
  const assignment = view?.assignment ?? null;
  const assignmentToken = assignment === null || view === null
    ? "none"
    : `${view.tournamentId}:${assignment.assignmentId}:${view.projectionRevision}`;
  const [state, setState] = useState(initialState);
  const inFlightRef = useRef(false);
  const requestRef = useRef(0);

  useEffect(() => {
    requestRef.current += 1;
    inFlightRef.current = false;
    setState(initialState);
  }, [assignmentToken]);

  const submit = useCallback((rawValue: string) => {
    const submittedFlag = rawValue;
    if (submittedFlag.trim().length === 0) {
      setState({ message: "Введите ответ перед отправкой.", status: "empty" });
      return;
    }

    if (view === null || assignment === null || !actionAllowed(view) || inFlightRef.current) {
      return;
    }

    inFlightRef.current = true;
    const requestId = ++requestRef.current;
    const intent: ParticipantSubmissionIntent = {
      gameId: assignment.gameId,
      projectionRevision: view.projectionRevision,
      seriesId: assignment.seriesId,
      submittedFlag,
      tournamentId: view.tournamentId,
    };
    setState({ message: "Проверяем ответ.", status: "pending" });

    void onSubmit(intent)
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
          message: "Не удалось отправить ответ. Повторите попытку.",
          status: "error",
        });
      })
      .finally(() => {
        if (requestId === requestRef.current) {
          inFlightRef.current = false;
        }
      });
  }, [assignment, onSubmit, view]);

  return { ...state, allowed: actionAllowed(view), submit };
};
