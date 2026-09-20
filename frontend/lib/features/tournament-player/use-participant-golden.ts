"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import {
  ApiError,
  createGoldenParticipantCommandIntent,
  goldenApi,
  type GoldenParticipantResponse,
} from "../../shared/api";

export type ParticipantGoldenLoadStatus =
  | "loading"
  | "ready"
  | "unavailable"
  | "error";

export type ParticipantGoldenActionStatus =
  | "idle"
  | "pending"
  | "accepted"
  | "incorrect"
  | "conflict"
  | "rate_limited"
  | "error";

export type ParticipantGoldenView = Readonly<{
  actionMessage: string | null;
  actionStatus: ParticipantGoldenActionStatus;
  loadMessage: string | null;
  loadStatus: ParticipantGoldenLoadStatus;
  ready: () => void;
  refresh: () => void;
  snapshot: GoldenParticipantResponse | null;
  submit: (submittedFlag: string) => void;
}>;

const isAbortLikeError = (value: unknown): boolean =>
  value instanceof DOMException &&
  (value.name === "AbortError" || value.name === "TimeoutError");

const loadMessageFor = (error: unknown): string => {
  if (error instanceof ApiError) {
    switch (error.status) {
      case 401:
        return "Сессия участника истекла. Войдите снова.";
      case 403:
        return "Сервер не разрешил доступ к этой Golden группе.";
      case 429:
        return "Слишком много запросов Golden. Повторите после паузы.";
      default:
        return "Не удалось получить состояние Golden. Повторите попытку.";
    }
  }
  return "Не удалось получить состояние Golden. Повторите попытку.";
};

const conflictMessage = "Golden попытка уже изменилась. Показано актуальное состояние сервера.";

export const useParticipantGolden = (
  tournamentId: string,
  refreshToken: string,
): ParticipantGoldenView => {
  const [snapshot, setSnapshot] = useState<GoldenParticipantResponse | null>(null);
  const [loadStatus, setLoadStatus] = useState<ParticipantGoldenLoadStatus>("loading");
  const [loadMessage, setLoadMessage] = useState<string | null>(null);
  const [actionStatus, setActionStatus] = useState<ParticipantGoldenActionStatus>("idle");
  const [actionMessage, setActionMessage] = useState<string | null>(null);
  const requestRef = useRef(0);
  const actionRef = useRef(0);
  const inFlightRef = useRef(false);
  const controllerRef = useRef<AbortController | null>(null);

  const refresh = useCallback(() => {
    const requestId = ++requestRef.current;
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    setLoadStatus((current) => current === "ready" ? current : "loading");
    setLoadMessage(null);

    void goldenApi.getParticipantState(tournamentId, controller.signal)
      .then((value) => {
        if (requestId !== requestRef.current || controller.signal.aborted) {
          return;
        }
        setSnapshot(value);
        setLoadStatus("ready");
        setLoadMessage(null);
      })
      .catch((error: unknown) => {
        if (
          requestId !== requestRef.current ||
          controller.signal.aborted ||
          isAbortLikeError(error)
        ) {
          return;
        }
        if (error instanceof ApiError && error.status === 404) {
          setSnapshot(null);
          setLoadStatus("unavailable");
          setLoadMessage(null);
          return;
        }
        setLoadStatus("error");
        setLoadMessage(loadMessageFor(error));
      });
  }, [tournamentId]);

  useEffect(() => {
    refresh();
    return () => {
      requestRef.current += 1;
      actionRef.current += 1;
      controllerRef.current?.abort();
      controllerRef.current = null;
      inFlightRef.current = false;
    };
  }, [refresh, refreshToken]);

  const ready = useCallback(() => {
    if (
      snapshot === null ||
      snapshot.ready ||
      snapshot.state !== "prepared" ||
      inFlightRef.current
    ) {
      return;
    }

    inFlightRef.current = true;
    const actionId = ++actionRef.current;
    setActionStatus("pending");
    setActionMessage("Подтверждаем готовность сервером.");
    void goldenApi.ready(
      tournamentId,
      {
        attempt_id: snapshot.attempt_id,
        expected_runtime_revision: snapshot.runtime_revision,
        ready: true,
        ready_window_id: snapshot.ready_window_id,
      },
      createGoldenParticipantCommandIntent(),
    )
      .then((result) => {
        if (actionId !== actionRef.current) {
          return;
        }
        if (result.status === "success") {
          setSnapshot(result.value);
          setActionStatus("accepted");
          setActionMessage("Готовность Golden подтверждена сервером.");
          return;
        }
        if (result.status === "conflict") {
          setSnapshot(result.snapshot);
          setActionStatus("conflict");
          setActionMessage(conflictMessage);
          return;
        }
        setActionStatus("rate_limited");
        setActionMessage(result.retryAfter
          ? `Слишком много попыток. Повторите после ${result.retryAfter}.`
          : "Слишком много попыток. Повторите после паузы.");
      })
      .catch(() => {
        if (actionId !== actionRef.current) {
          return;
        }
        setActionStatus("error");
        setActionMessage("Не удалось подтвердить готовность Golden.");
      })
      .finally(() => {
        if (actionId === actionRef.current) {
          inFlightRef.current = false;
        }
      });
  }, [snapshot, tournamentId]);

  const submit = useCallback((submittedFlag: string) => {
    if (submittedFlag.trim().length === 0) {
      setActionStatus("error");
      setActionMessage("Введите ответ перед отправкой.");
      return;
    }
    if (
      snapshot === null ||
      snapshot.state !== "active" ||
      snapshot.task === null ||
      snapshot.submitted ||
      inFlightRef.current
    ) {
      return;
    }

    inFlightRef.current = true;
    const actionId = ++actionRef.current;
    setActionStatus("pending");
    setActionMessage("Проверяем ответ сервером.");
    void goldenApi.submit(
      tournamentId,
      {
        attempt_id: snapshot.attempt_id,
        expected_runtime_revision: snapshot.runtime_revision,
        ready_window_id: snapshot.ready_window_id,
        submitted_flag: submittedFlag,
      },
      createGoldenParticipantCommandIntent(),
    )
      .then((result) => {
        if (actionId !== actionRef.current) {
          return;
        }
        if (result.status === "success") {
          setSnapshot(result.value);
          if (result.value.submitted) {
            setActionStatus("accepted");
            setActionMessage("Решение Golden принято сервером.");
          } else {
            setActionStatus("incorrect");
            setActionMessage("Ответ неверный. Сервер сохранил попытку.");
          }
          return;
        }
        if (result.status === "conflict") {
          setSnapshot(result.snapshot);
          setActionStatus("conflict");
          setActionMessage(conflictMessage);
          return;
        }
        setActionStatus("rate_limited");
        setActionMessage(result.retryAfter
          ? `Слишком много попыток. Повторите после ${result.retryAfter}.`
          : "Слишком много попыток. Повторите после паузы.");
      })
      .catch(() => {
        if (actionId !== actionRef.current) {
          return;
        }
        setActionStatus("error");
        setActionMessage("Не удалось отправить ответ Golden.");
      })
      .finally(() => {
        if (actionId === actionRef.current) {
          inFlightRef.current = false;
        }
      });
  }, [snapshot, tournamentId]);

  return {
    actionMessage,
    actionStatus,
    loadMessage,
    loadStatus,
    ready,
    refresh,
    snapshot,
    submit,
  };
};
