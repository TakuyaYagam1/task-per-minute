"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import {
  ApiError,
  participantApi,
  type ParticipantAssignmentResponse,
  ParticipantSourceFileURLPolicyError,
  type ParticipantSourceFileResponse,
} from "../../shared/api";

import type { ParticipantPlayerView } from "./model";

export type ParticipantSourceFileStatus = "idle" | "loading" | "ready" | "error";

export type ParticipantSourceFileView = Readonly<{
  message: string | null;
  request: () => void;
  status: ParticipantSourceFileStatus;
}>;

export type ParticipantSourceFileTarget = Readonly<{
  assignmentId: string;
  sourceFileAvailable: boolean;
  tournamentId: string;
  verifyAssignment?: boolean;
}>;

const EXPIRY_SKEW_MS = 1_000;

const initialState: Omit<ParticipantSourceFileView, "request"> = {
  message: null,
  status: "idle",
};

class ParticipantSourceFileExpiredError extends Error {
  constructor() {
    super("Participant source file URL is expired");
    this.name = "ParticipantSourceFileExpiredError";
  }
}

class ParticipantAssignmentChangedError extends Error {
  constructor() {
    super("Participant assignment changed while loading the source file");
    this.name = "ParticipantAssignmentChangedError";
  }
}

class ParticipantSourceFileUnavailableError extends Error {
  constructor() {
    super("Participant assignment does not expose a source file");
    this.name = "ParticipantSourceFileUnavailableError";
  }
}

const isAbortLikeError = (value: unknown): boolean =>
  value instanceof DOMException &&
  (value.name === "AbortError" || value.name === "TimeoutError");

const isExpired = (expiresAt: string): boolean => {
  const timestamp = Date.parse(expiresAt);
  return !Number.isFinite(timestamp) || timestamp <= Date.now() + EXPIRY_SKEW_MS;
};

const sourceFileMessageFor = (error: unknown): string => {
  if (error instanceof ParticipantSourceFileURLPolicyError) {
    return "Ссылка на архив недоступна.";
  }
  if (error instanceof ParticipantSourceFileExpiredError) {
    return "Ссылка на архив уже истекла. Запросите новую ссылку.";
  }
  if (error instanceof ParticipantAssignmentChangedError) {
    return "Задание изменилось. Обновите матч и повторите попытку.";
  }
  if (error instanceof ParticipantSourceFileUnavailableError) {
    return "Архив для этого задания недоступен.";
  }
  if (error instanceof ApiError) {
    switch (error.status) {
      case 401:
        return "Сессия участника истекла. Войдите снова.";
      case 403:
        return "Скачивание архива недоступно для этого задания.";
      case 404:
        return "Архив для этого задания не найден.";
      default:
        return "Не удалось получить ссылку на архив. Повторите попытку.";
    }
  }
  return "Не удалось получить ссылку на архив. Повторите попытку.";
};

const readFreshSourceFile = async (
  tournamentId: string,
  assignmentId: string,
  verifyAssignment: boolean,
  signal: AbortSignal,
): Promise<ParticipantSourceFileResponse> => {
  const readCurrentAssignment = async (): Promise<ParticipantAssignmentResponse> => {
    const response = await participantApi.getAssignment(tournamentId, assignmentId, signal);
    if (response.assignment.id !== assignmentId) {
      throw new ParticipantAssignmentChangedError();
    }
    if (response.assignment.active_snapshot.source_file_available !== true) {
      throw new ParticipantSourceFileUnavailableError();
    }
    return response;
  };

  let assignmentResponse = verifyAssignment ? await readCurrentAssignment() : null;
  let sourceFile = await participantApi.getAssignmentSourceFile(
    tournamentId,
    assignmentResponse?.assignment.id ?? assignmentId,
    signal,
  );

  if (isExpired(sourceFile.expires_at)) {
    assignmentResponse = verifyAssignment ? await readCurrentAssignment() : null;
    sourceFile = await participantApi.getAssignmentSourceFile(
      tournamentId,
      assignmentResponse?.assignment.id ?? assignmentId,
      signal,
    );
  }

  if (isExpired(sourceFile.expires_at)) {
    throw new ParticipantSourceFileExpiredError();
  }
  return sourceFile;
};

const triggerArchiveDownload = (sourceFileURL: string): void => {
  window.location.assign(sourceFileURL);
};

/**
 * Normal assignments are revalidated before each source-file request. Golden
 * targets can skip that lookup because their immutable assignment is exposed
 * only by the Golden participant contract.
 * The validated URL is used only for the browser download navigation and is
 * never part of recovery state, browser storage, or rendered React state.
 */
export const useParticipantAssignmentSourceFile = (
  target: ParticipantSourceFileTarget | null,
): ParticipantSourceFileView => {
  const assignmentToken = target === null
    ? "none"
    : `${target.tournamentId}:${target.assignmentId}`;
  const [state, setState] = useState(initialState);
  const requestRef = useRef(0);
  const controllerRef = useRef<AbortController | null>(null);

  useEffect(() => {
    requestRef.current += 1;
    controllerRef.current?.abort();
    controllerRef.current = null;
    setState(initialState);

    return () => {
      requestRef.current += 1;
      controllerRef.current?.abort();
      controllerRef.current = null;
    };
  }, [assignmentToken]);

  const request = useCallback(() => {
    if (
      target === null ||
      !target.sourceFileAvailable ||
      state.status === "loading"
    ) {
      return;
    }

    const requestId = ++requestRef.current;
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    setState({ ...initialState, status: "loading" });

    void readFreshSourceFile(
      target.tournamentId,
      target.assignmentId,
      target.verifyAssignment !== false,
      controller.signal,
    )
      .then((sourceFile) => {
        if (requestId !== requestRef.current || controller.signal.aborted) {
          return;
        }
        triggerArchiveDownload(sourceFile.source_file_url);
        setState({
          message: "Архив отправлен на скачивание.",
          status: "ready",
        });
      })
      .catch((error: unknown) => {
        if (
          requestId !== requestRef.current ||
          controller.signal.aborted ||
          isAbortLikeError(error)
        ) {
          return;
        }
        setState({
          ...initialState,
          message: sourceFileMessageFor(error),
          status: "error",
        });
      });
  }, [state.status, target]);

  return { ...state, request };
};

export const useParticipantSourceFile = (
  view: ParticipantPlayerView | null,
): ParticipantSourceFileView => {
  const assignment = view?.assignment ?? null;
  const target = assignment === null || view === null
    ? null
    : {
        assignmentId: assignment.assignmentId,
        sourceFileAvailable: assignment.sourceFileAvailable,
        tournamentId: view.tournamentId,
      };
  return useParticipantAssignmentSourceFile(target);
};
