"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import {
  ApiError,
  getTournamentContent,
  type TournamentContentSelection,
} from "../../shared/api";

import {
  TournamentContentManager,
  type AdminRequestRunner,
} from "./TournamentContentManager";
import styles from "./TournamentTasksSection.module.css";

type LoadState = "loading" | "ready" | "error";

export type TournamentTasksSectionProps = Readonly<{
  onSessionExpired?: () => void;
  onDirtyChange?: (dirty: boolean) => void;
  runAdminRequest?: AdminRequestRunner;
}>;

const isAbortError = (error: unknown): boolean =>
  error instanceof DOMException &&
  (error.name === "AbortError" || error.name === "TimeoutError");

const problemMessage = (error: unknown, fallback: string): string => {
  if (error instanceof ApiError) {
    return error.problem?.detail || error.problem?.title || fallback;
  }
  return fallback;
};

export const TournamentTasksSection = ({
  onDirtyChange,
  onSessionExpired,
  runAdminRequest,
}: TournamentTasksSectionProps) => {
  const [content, setContent] = useState<TournamentContentSelection | null>(null);
  const [contentState, setContentState] = useState<LoadState>("loading");
  const [contentError, setContentError] = useState<string | null>(null);
  const [contentEmpty, setContentEmpty] = useState(false);
  const controllerRef = useRef<AbortController | null>(null);
  const requestGenerationRef = useRef(0);

  const loadContent = useCallback(async (): Promise<void> => {
    controllerRef.current?.abort();
    const controller = new AbortController();
    const requestGeneration = requestGenerationRef.current + 1;
    requestGenerationRef.current = requestGeneration;
    controllerRef.current = controller;
    setContentState("loading");
    setContentError(null);
    setContentEmpty(false);

    try {
      const selection = await getTournamentContent(controller.signal);
      if (
        controller.signal.aborted ||
        requestGenerationRef.current !== requestGeneration
      ) {
        return;
      }
      setContent(selection);
      setContentState("ready");
    } catch (error) {
      if (
        controller.signal.aborted ||
        requestGenerationRef.current !== requestGeneration ||
        isAbortError(error)
      ) {
        return;
      }
      if (error instanceof ApiError && error.status === 401) {
        onSessionExpired?.();
      }
      const unavailable = error instanceof ApiError && error.status === 422;
      setContent(null);
      setContentEmpty(unavailable);
      setContentState("error");
      setContentError(
        unavailable
          ? "Опубликованных задач пока нет. Сначала опубликуйте задачи."
          : problemMessage(error, "Не удалось получить текущую публикацию контента"),
      );
    } finally {
      if (controllerRef.current === controller) {
        controllerRef.current = null;
      }
    }
  }, [onSessionExpired]);

  useEffect(() => {
    void loadContent();
    return () => {
      controllerRef.current?.abort();
      requestGenerationRef.current += 1;
    };
  }, [loadContent]);

  return (
    <section className={styles.root} aria-label="Задания">
      <TournamentContentManager
        content={content}
        contentEmpty={contentEmpty}
        contentState={contentState}
        contentError={contentError}
        onDirtyChange={onDirtyChange}
        onReloadContent={() => void loadContent()}
        onSessionExpired={onSessionExpired}
        runAdminRequest={runAdminRequest}
      />
    </section>
  );
};

TournamentTasksSection.displayName = "TournamentTasksSection";
