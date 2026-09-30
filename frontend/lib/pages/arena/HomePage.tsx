"use client";

import Link from "next/link";
import { useEffect, useState } from "react";

import { playerModel } from "../../entities/player";
import { getSafeArenaReturnPath, useTimedNotification } from "../../shared/lib";
import type { Player } from "../../shared/types";
import { ViewportPortal } from "../../shared/ui";

import styles from "./HomePage.module.css";

const participantReturnPath = (): string | null =>
  getSafeArenaReturnPath(
    new URLSearchParams(window.location.search).get("next"),
    "participant",
  );

export default function HomePage() {
  const [currentPlayer, setCurrentPlayer] = useState<Player | null>(null);
  const [isRestoring, setIsRestoring] = useState(true);
  const [isLeaving, setIsLeaving] = useState(false);
  const [loginHref, setLoginHref] = useState("/login");
  const [registerHref, setRegisterHref] = useState("/register");
  const { notification, showNotification } = useTimedNotification<string>();

  useEffect(() => {
    const returnPath = participantReturnPath();
    if (returnPath) {
      const next = encodeURIComponent(returnPath);
      setLoginHref(`/login?next=${next}`);
      setRegisterHref(`/register?next=${next}`);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    void (async () => {
      const result = await playerModel.restoreCurrentPlayer(controller.signal);
      if (cancelled || result.kind === "aborted") {
        return;
      }
      setIsRestoring(false);
      if (result.kind === "ok") {
        setCurrentPlayer(result.state.player);
        const returnPath = participantReturnPath();
        if (returnPath) {
          window.location.replace(returnPath);
        }
        return;
      }
      setCurrentPlayer(null);
      if (result.kind === "contract") {
        showNotification("Не удалось проверить сессию. Попробуйте позже.");
      } else if (result.kind === "error") {
        showNotification("Не удалось подключиться. Попробуйте позже.");
      }
    })();

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [showNotification]);

  const handleChangePlayer = async () => {
    if (!currentPlayer || isLeaving) {
      return;
    }
    setIsLeaving(true);
    await playerModel.clearCurrentPlayer();
    setCurrentPlayer(null);
    setIsLeaving(false);
    showNotification("Сессия завершена.");
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

              {isRestoring ? (
                <p className={styles.restoreMessage} role="status">Проверяем сессию...</p>
              ) : currentPlayer ? (
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
                    {isLeaving ? "Завершение сессии..." : "Выйти"}
                  </button>
                  <div className={styles.sessionState} aria-live="polite">
                    <span className={`${styles.stateDot} ${styles.stateDotReady}`} aria-hidden="true" />
                    Игрок готов
                  </div>
                </div>
              ) : (
                <div className={styles.authLinks}>
                  <p className={styles.authCopy}>
                    Войдите или создайте аккаунт, чтобы участвовать в соревнованиях.
                  </p>
                  <Link href={loginHref} className={`${styles.primaryButton} btn btn-primary`}>
                    Войти
                  </Link>
                  <Link href={registerHref} className={`${styles.secondaryButton} btn btn-secondary`}>
                    Создать аккаунт
                  </Link>
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
