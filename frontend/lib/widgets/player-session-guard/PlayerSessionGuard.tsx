"use client";

import { usePathname, useRouter } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";

import {
  acceptPlayerAccountDeletedHint,
  getPlayerSessionEpoch,
  isCurrentPlayerSessionEpoch,
  isPlayerSessionRotationInProgress,
  PLAYER_ACCOUNT_DELETED_EVENT,
  PLAYER_SESSION_CHANNEL,
  playerApi,
  takePlayerAccountDeletedNotice,
  type PlayerAccountDeletedNotice,
} from "@/shared/api";
import { playerStorage } from "@/shared/lib";
import { Button, Dialog } from "@/shared/ui";

const PLAYER_ME_POLL_INTERVAL_MS = 10_000;
const PLAYER_ME_TIMEOUT_MS = 7_000;

type PlayerAccountDeletedBroadcast = Readonly<{
  type: "player.account_deleted";
  playerId: string;
}>;

const isPlayerAccountDeletedBroadcast = (
  value: unknown,
): value is PlayerAccountDeletedBroadcast =>
  Boolean(value) &&
  typeof value === "object" &&
  !Array.isArray(value) &&
  (value as Record<string, unknown>).type === "player.account_deleted" &&
  typeof (value as Record<string, unknown>).playerId === "string";

const isAdminPath = (pathname: string): boolean =>
  pathname === "/admin" || pathname.startsWith("/admin/");

export function PlayerSessionGuard() {
  const pathname = usePathname();
  const router = useRouter();
  const [deletedNotice, setDeletedNotice] =
    useState<PlayerAccountDeletedNotice | null>(null);
  const confirmButtonRef = useRef<HTMLButtonElement>(null);
  const requestRef = useRef<{
    controller: AbortController;
    epoch: number;
    playerId: string;
  } | null>(null);
  const isAdmin = isAdminPath(pathname);

  const checkPlayerSession = useCallback(async () => {
    if (
      isAdmin ||
      isPlayerSessionRotationInProgress() ||
      document.visibilityState !== "visible" ||
      requestRef.current !== null
    ) {
      return;
    }

    const playerId = playerStorage.getPlayerId();
    if (!playerId) {
      return;
    }

    const epoch = getPlayerSessionEpoch();
    const controller = new AbortController();
    const request = { controller, epoch, playerId };
    requestRef.current = request;
    let timeoutId: number | null = null;

    try {
      const timeout = new Promise<never>((_resolve, reject) => {
        timeoutId = window.setTimeout(() => {
          controller.abort();
          reject(new DOMException("Player session check timed out", "TimeoutError"));
        }, PLAYER_ME_TIMEOUT_MS);
      });
      const current = await Promise.race([
        playerApi.me(controller.signal),
        timeout,
      ]);
      if (
        controller.signal.aborted ||
        !isCurrentPlayerSessionEpoch(epoch) ||
        playerStorage.getPlayerId() !== playerId
      ) {
        return;
      }

      // A different tab may have replaced the cookie-backed identity. Do not
      // apply that response to this tab's cached player identity.
      if (current.player.id !== playerId) {
        return;
      }
    } catch {
      // The shared API client surfaces only the exact deletion code. Ordinary
      // expiry and transport failures remain silent here.
    } finally {
      if (timeoutId !== null) {
        window.clearTimeout(timeoutId);
      }
      if (requestRef.current === request) {
        requestRef.current = null;
      }
    }
  }, [isAdmin]);

  useEffect(() => {
    if (isAdmin) {
      return undefined;
    }

    const handleDeletedEvent = (event: Event): void => {
      const detail = (event as CustomEvent<PlayerAccountDeletedNotice>).detail;
      if (
        !detail ||
        (detail.playerId !== null && typeof detail.playerId !== "string") ||
        !Number.isInteger(detail.epoch) ||
        !isCurrentPlayerSessionEpoch(detail.epoch)
      ) {
        return;
      }
      setDeletedNotice(detail);
    };

    let channel: BroadcastChannel | null = null;
    if (typeof BroadcastChannel !== "undefined") {
      try {
        channel = new BroadcastChannel(PLAYER_SESSION_CHANNEL);
        channel.onmessage = (event: MessageEvent<unknown>) => {
          if (!isPlayerAccountDeletedBroadcast(event.data)) {
            return;
          }
          const currentPlayerId = playerStorage.getPlayerId();
          if (currentPlayerId !== event.data.playerId) {
            return;
          }
          acceptPlayerAccountDeletedHint(
            event.data.playerId,
            getPlayerSessionEpoch(),
          );
        };
      } catch {
        channel = null;
      }
    }

    window.addEventListener(PLAYER_ACCOUNT_DELETED_EVENT, handleDeletedEvent);
    const pendingNotice = takePlayerAccountDeletedNotice();
    if (pendingNotice && isCurrentPlayerSessionEpoch(pendingNotice.epoch)) {
      setDeletedNotice(pendingNotice);
    }
    const interval = window.setInterval(
      () => void checkPlayerSession(),
      PLAYER_ME_POLL_INTERVAL_MS,
    );
    const handleFocus = (): void => void checkPlayerSession();
    const handleVisibility = (): void => {
      if (document.visibilityState === "visible") {
        void checkPlayerSession();
      }
    };
    window.addEventListener("focus", handleFocus);
    document.addEventListener("visibilitychange", handleVisibility);

    return () => {
      window.removeEventListener(PLAYER_ACCOUNT_DELETED_EVENT, handleDeletedEvent);
      window.removeEventListener("focus", handleFocus);
      document.removeEventListener("visibilitychange", handleVisibility);
      window.clearInterval(interval);
      channel?.close();
      requestRef.current?.controller.abort();
      requestRef.current = null;
    };
  }, [checkPlayerSession, isAdmin]);

  const confirmDeletion = (): void => {
    takePlayerAccountDeletedNotice();
    setDeletedNotice(null);
    router.replace("/login");
  };

  if (isAdmin || deletedNotice === null) {
    return null;
  }

  return (
    <Dialog
      open
      title="Аккаунт удален"
      size="small"
      closeOnEscape={false}
      closeOnBackdrop={false}
      showCloseButton={false}
      initialFocusRef={confirmButtonRef}
    >
      <p>Администратор удалил ваш аккаунт.</p>
      <Button ref={confirmButtonRef} onClick={confirmDeletion}>
        Понятно
      </Button>
    </Dialog>
  );
}
