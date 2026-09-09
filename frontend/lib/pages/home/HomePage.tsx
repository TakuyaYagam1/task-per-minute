"use client";

import Image from "next/image";
import Link from "next/link";
import { FormEvent, useEffect, useState } from "react";

import { playerModel } from "../../entities/player";
import { isValidUsername, log, useTimedNotification } from "../../shared/lib";
import type { Player } from "../../shared/types";
import { ViewportPortal } from "../../shared/ui";

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
      return;
    }
    if (result.kind === "rate_limited") {
      showNotification(buildRateLimitMessage(result.retryAfter));
      return;
    }
    if (result.kind !== "aborted") {
      showNotification("Ошибка подключения к серверу");
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
          <div className="pointer-events-none fixed right-4 top-4 z-50 flex max-w-[calc(100vw-2rem)] justify-end sm:right-6 sm:top-6">
            <div className="pointer-events-auto max-w-md rounded-lg bg-black/80 px-4 py-2 text-white animate-fadeIn">
              {notification}
            </div>
          </div>
        </ViewportPortal>
      )}

      <main className="relative flex min-h-screen flex-col items-center justify-center p-3 animate-fadeIn gpu-optimized lg:p-6">
        <div className="container max-w-6xl">
          <div className="card overflow-hidden animate-scaleIn will-change-transform">
            <div className="flex flex-col lg:flex-row">
              <div className="relative overflow-hidden lg:w-2/3">
                <Image
                  src="/task.png"
                  alt="Task Per Minute"
                  width={900}
                  height={600}
                  className="h-72 w-full object-cover animate-slideInLeft will-change-transform sm:h-64 lg:h-full"
                  priority
                />
                <div className="absolute inset-0 bg-gradient-to-t from-black/60 via-transparent to-transparent lg:bg-gradient-to-r lg:from-transparent lg:via-transparent lg:to-black/60" />
                <div className="absolute inset-x-0 bottom-6 z-10 flex justify-center px-4">
                  <Link
                    href="/leaderboard"
                    className="btn btn-secondary !w-auto !min-w-[140px] border-white/35 bg-white/15 !px-4 !py-2 text-sm font-bold uppercase leading-none text-white shadow-lg backdrop-blur-md hover:bg-white/25 active:scale-95 lg:!min-w-[180px] lg:!px-5 lg:!py-3 lg:text-base"
                  >
                    Лидерборд
                  </Link>
                </div>
              </div>

              <div className="flex flex-col justify-center p-6 animate-slideInRight lg:w-1/3 lg:p-8">
                <h1 className="mb-4 text-center text-2xl font-bold lg:mb-6 lg:text-3xl xl:text-4xl">
                  Task Per Minute
                </h1>
                <h2 className="mb-4 text-center text-lg font-semibold text-blue-200 lg:text-xl">
                  CTF турнир
                </h2>
                <p className="mb-6 text-center text-sm leading-relaxed text-gray-300 lg:mb-8 lg:text-base">
                  Войдите как участник, чтобы сохранить браузерную сессию.
                </p>

                {!currentPlayer ? (
                  <form onSubmit={handleJoin} className="mb-4">
                    <div className="flex flex-col gap-3">
                      <input
                        type="text"
                        value={nickname}
                        onChange={(event) => setNickname(event.target.value)}
                        placeholder="Введите никнейм..."
                        maxLength={50}
                        className="w-full rounded-lg border border-white/20 bg-white/10 px-4 py-2.5 text-sm text-white placeholder-gray-400 transition-all focus:border-transparent focus:outline-none focus:ring-2 focus:ring-blue-400"
                        disabled={isJoining}
                      />
                      <button
                        type="submit"
                        disabled={isJoining || !nickname.trim()}
                        className="btn btn-primary w-full px-8 py-4 text-lg font-bold transition-all duration-300 ease-out disabled:cursor-not-allowed disabled:opacity-50 hover:scale-105 hover:shadow-lg active:scale-95"
                      >
                        {isJoining ? "ПОДКЛЮЧЕНИЕ..." : "ПОДКЛЮЧИТЬСЯ"}
                      </button>
                    </div>
                  </form>
                ) : (
                  <div className="mb-4 flex flex-col gap-3">
                    <div className="rounded-lg border border-white/15 bg-white/10 px-4 py-3 text-center">
                      <div className="text-xs uppercase tracking-wide text-gray-300">
                        Текущий игрок
                      </div>
                      <div className="mt-1 font-semibold text-white">
                        {currentPlayer.username}
                      </div>
                    </div>
                    <button
                      type="button"
                      onClick={handleChangePlayer}
                      disabled={isLeaving}
                      className="btn btn-secondary w-full px-5 py-3 text-sm font-bold transition-all duration-300 ease-out disabled:cursor-not-allowed disabled:opacity-50 hover:scale-105 hover:shadow-lg active:scale-95"
                    >
                      {isLeaving ? "Смена игрока..." : "Сменить игрока"}
                    </button>
                  </div>
                )}

                <div className="text-xs text-gray-400">
                  <div className="flex items-center justify-center gap-2">
                    <div
                      className={`h-2 w-2 rounded-full ${
                        currentPlayer ? "bg-green-400" : "bg-yellow-400"
                      }`}
                    />
                    {currentPlayer ? "Игрок готов" : "Введите никнейм"}
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </main>
    </>
  );
}
