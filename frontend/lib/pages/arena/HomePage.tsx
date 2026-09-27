"use client";

import Link from "next/link";
import { type FormEvent, useEffect, useState } from "react";

import { playerModel } from "../../entities/player";
import {
  getSafeArenaReturnPath,
  isValidUsername,
  log,
  useTimedNotification,
} from "../../shared/lib";
import type { Player } from "../../shared/types";
import { ViewportPortal } from "../../shared/ui";

import styles from "./HomePage.module.css";

const buildRateLimitMessage = (retryAfter?: string | null): string => {
  const value = retryAfter?.trim();
  if (!value) {
    return "Слишком много попыток. Повторите чуть позже.";
  }
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds > 0) {
    return `Слишком много попыток. Повторите через ${Math.ceil(seconds)} секунд.`;
  }
  return "Слишком много попыток. Повторите позже.";
};

const participantReturnPath = (): string | null =>
  getSafeArenaReturnPath(
    new URLSearchParams(window.location.search).get("next"),
    "participant",
  );

export default function HomePage() {
  const [nickname, setNickname] = useState("");
  const [currentPlayer, setCurrentPlayer] = useState<Player | null>(null);
  const [isJoining, setIsJoining] = useState(false);
  const [isLeaving, setIsLeaving] = useState(false);
  const { notification, showNotification } = useTimedNotification<string>();

  useEffect(() => {
    const player = playerModel.getCurrentPlayer();
    if (!player) {
      return;
    }

    setCurrentPlayer(player);
    setNickname(player.username);

    const controller = new AbortController();
    let cancelled = false;
    void (async () => {
      const result = await playerModel.refreshCurrentPlayer(
        player,
        controller.signal,
      );
      if (cancelled || result.kind === "aborted") {
        return;
      }
      if (result.kind === "ok") {
        setCurrentPlayer(result.state.player);
        setNickname(result.state.player.username);
        const returnPath = participantReturnPath();
        if (returnPath) {
          window.location.replace(returnPath);
        }
        return;
      }
      if (result.kind === "expired") {
        setCurrentPlayer(null);
        setNickname("");
        showNotification("Сессия истекла. Введите никнейм заново.");
        return;
      }
      if (result.kind === "contract") {
        log.warn("players/me returned malformed payload");
        showNotification("Не удалось проверить сессию. Попробуйте еще раз.");
        return;
      }
      log.warn("players/me failed while restoring the player session");
    })();

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [showNotification]);

  const handleJoin = async (event: FormEvent) => {
    event.preventDefault();
    const username = nickname.trim();
    if (!isValidUsername(username)) {
      showNotification("Никнейм: 2-50 символов, латиница, цифры, _ или -");
      return;
    }

    setIsJoining(true);
    const result = await playerModel.initializePlayer(username);
    setIsJoining(false);

    if (result.kind === "ok") {
      setCurrentPlayer(result.player);
      setNickname(result.player.username);
      const returnPath = participantReturnPath();
      if (returnPath) {
        window.location.replace(returnPath);
      }
      return;
    }
    if (result.kind === "rate_limited") {
      showNotification(buildRateLimitMessage(result.retryAfter));
      return;
    }
    if (result.kind !== "aborted") {
      showNotification("Ошибка соединения");
    }
  };

  const handleChangePlayer = async () => {
    if (!currentPlayer || isLeaving) {
      return;
    }
    setIsLeaving(true);
    await playerModel.clearCurrentPlayer();
    setCurrentPlayer(null);
    setNickname("");
    setIsLeaving(false);
    showNotification("Сессия отменена.");
  };

  return (
    <>
      {notification && (
        <ViewportPortal>
          <div className={styles.notificationWrap}>
            <div className={styles.notification} role="status" aria-live="polite">
              {notification}
            </div>
          </div>
        </ViewportPortal>
      )}

      <main className={styles.page}>
        <div className={styles.shell}>
          <section className={`${styles.homeCard} card`} aria-labelledby="home-title">
            <div className={styles.intro}>
              <h1 className={styles.title} id="home-title">Task Per Minute</h1>
              <h2 className={styles.subtitle}>CTF Соревнования</h2>
            </div>

            <section className={styles.joinPanel} aria-labelledby="join-title">
              <h2 className={styles.sectionTitle} id="join-title">Вход участника</h2>

              {!currentPlayer ? (
                <form onSubmit={handleJoin} className={styles.form}>
                  <label className={styles.srOnly} htmlFor="nickname">
                    Никнейм
                  </label>
                  <input
                    id="nickname"
                    type="text"
                    value={nickname}
                    onChange={(event) => setNickname(event.target.value)}
                    placeholder="Введите никнейм..."
                    maxLength={50}
                    autoComplete="nickname"
                    className={styles.input}
                    disabled={isJoining}
                  />
                  <button
                    type="submit"
                    disabled={isJoining || !nickname.trim()}
                    className={`${styles.primaryButton} btn btn-primary`}
                  >
                    {isJoining ? "ПОДКЛЮЧЕНИЕ..." : "ПОДКЛЮЧИТЬСЯ"}
                  </button>
                </form>
              ) : (
                <div className={styles.currentPlayer}>
                  <div className={styles.playerSummary}>
                    <span className={styles.playerLabel}>Текущий игрок</span>
                    <strong>{currentPlayer.username}</strong>
                  </div>
                  <button
                    type="button"
                    onClick={handleChangePlayer}
                    disabled={isLeaving}
                    className={`${styles.secondaryButton} btn btn-secondary`}
                  >
                    {isLeaving ? "Смена игрока..." : "Сменить игрока"}
                  </button>
                </div>
              )}

              {currentPlayer && (
                <div className={styles.sessionState} aria-live="polite">
                  <span className={`${styles.stateDot} ${styles.stateDotReady}`} aria-hidden="true" />
                  Игрок готов
                </div>
              )}

              <Link href="/arena" className={`${styles.arenaLink} btn btn-secondary`}>
                Открыть Arena
              </Link>
            </section>
          </section>
        </div>
      </main>
    </>
  );
}
