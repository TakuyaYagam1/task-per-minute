"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";

import { playerModel } from "../../entities/player";
import { getSafeArenaPublicReturnPath, getSafeArenaReturnPath } from "../../shared/lib";
import { Button, Message, Panel } from "../../shared/ui";

import styles from "./PlayerEntryPage.module.css";

type EntryStatus = "redirecting" | "checking" | "error";

const requestedReturnPath = (): string | null =>
  typeof window === "undefined"
    ? null
    : new URLSearchParams(window.location.search).get("next");

const safeParticipantReturnPath = (): string | null =>
  getSafeArenaReturnPath(requestedReturnPath(), "participant");

const safePublicReturnPath = (): string | null =>
  getSafeArenaPublicReturnPath(requestedReturnPath());

export function PlayerEntryPage() {
  const router = useRouter();
  const [status, setStatus] = useState<EntryStatus>("redirecting");
  const requestRef = useRef<AbortController | null>(null);

  const checkSession = useCallback(async (
    controller: AbortController,
    returnPath: string,
  ): Promise<void> => {
    setStatus("checking");
    try {
      const result = await playerModel.restoreCurrentPlayer(controller.signal);
      if (controller.signal.aborted || requestRef.current !== controller || result.kind === "aborted") return;

      if (result.kind === "ok") {
        router.replace(returnPath);
        return;
      }
      if (result.kind === "expired") {
        router.replace(`/login?next=${encodeURIComponent(returnPath)}`);
        return;
      }
      setStatus("error");
    } catch {
      if (!controller.signal.aborted && requestRef.current === controller) {
        setStatus("error");
      }
    } finally {
      if (requestRef.current === controller) requestRef.current = null;
    }
  }, [router]);

  useEffect(() => {
    const publicPath = safePublicReturnPath();
    if (publicPath) {
      router.replace(publicPath);
      return undefined;
    }

    const participantPath = safeParticipantReturnPath();
    if (!participantPath) {
      router.replace("/arena");
      return undefined;
    }

    const controller = new AbortController();
    requestRef.current = controller;
    void checkSession(controller, participantPath);
    return () => {
      requestRef.current?.abort();
      requestRef.current = null;
    };
  }, [checkSession, router]);

  const retry = (): void => {
    const participantPath = safeParticipantReturnPath();
    if (!participantPath) {
      router.replace("/arena");
      return;
    }
    requestRef.current?.abort();
    const controller = new AbortController();
    requestRef.current = controller;
    void checkSession(controller, participantPath);
  };

  if (status === "redirecting") return null;

  return (
    <main className={styles.page} aria-busy={status === "checking"}>
      {status === "checking" ? (
        <div className={styles.content}>
          <Message title="Проверяем сессию" tone="loading" />
        </div>
      ) : (
        <div className={styles.content}>
          <Panel as="section" title="Не удалось проверить сессию" tone="error">
            <div className={styles.errorContent}>
              <p>Проверьте подключение и попробуйте еще раз.</p>
              <Button onClick={retry}>Повторить</Button>
            </div>
          </Panel>
        </div>
      )}
    </main>
  );
}

PlayerEntryPage.displayName = "PlayerEntryPage";
