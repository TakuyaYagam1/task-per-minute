'use client';
import React, { useState, useEffect, useRef } from 'react';
import Link from 'next/link';
import styles from './leaderboard.module.css';
import { ApiError, leaderboardApi } from '../../lib/shared/api';

interface LeaderboardEntry {
  rank: number;
  username: string;
  wins: number;
  average_solve_time_ms: number;
}

const formatTime = (ms: number): string => {
  const totalSeconds = ms / 1000;
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = (totalSeconds % 60).toFixed(1);
  if (minutes > 0) {
    return `${minutes}м ${seconds}с`;
  }
  return `${seconds}с`;
};

export default function Leaderboard() {
  const [entries, setEntries] = useState<LeaderboardEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const entriesRef = useRef<LeaderboardEntry[]>([]);
  const requestIDRef = useRef(0);
  const lastHandledRequestIDRef = useRef(0);

  useEffect(() => {
    let isMounted = true;
    const controller = new AbortController();
    let pollInterval: ReturnType<typeof setInterval> | null = null;
    let backoffTimer: ReturnType<typeof setTimeout> | null = null;

    const BASE_INTERVAL_MS = 5_000;
    const MAX_BACKOFF_MS = 60_000;

    const isAbortError = (error: unknown): boolean =>
      error instanceof DOMException &&
      (error.name === 'AbortError' || error.name === 'TimeoutError');

    const computeRetryDelayMs = (raw: string | null | undefined): number => {
      if (!raw) {
        return BASE_INTERVAL_MS;
      }
      const seconds = Number(raw);
      if (Number.isFinite(seconds) && seconds >= 0) {
        return Math.min(MAX_BACKOFF_MS, Math.max(BASE_INTERVAL_MS, Math.round(seconds * 1000)));
      }
      const epoch = Date.parse(raw);
      if (!Number.isFinite(epoch)) {
        return BASE_INTERVAL_MS;
      }
      const diff = Math.max(0, epoch - Date.now());
      return Math.min(MAX_BACKOFF_MS, Math.max(BASE_INTERVAL_MS, diff));
    };

    const startInterval = () => {
      if (pollInterval !== null) {
        clearInterval(pollInterval);
      }
      pollInterval = setInterval(fetchLeaderboard, BASE_INTERVAL_MS);
    };

    const applyBackoff = (delayMs: number) => {
      if (pollInterval !== null) {
        clearInterval(pollInterval);
        pollInterval = null;
      }
      if (backoffTimer !== null) {
        clearTimeout(backoffTimer);
      }
      backoffTimer = setTimeout(() => {
        backoffTimer = null;
        if (!isMounted) {
          return;
        }
        fetchLeaderboard();
        startInterval();
      }, delayMs);
    };

    const fetchLeaderboard = async () => {
      const requestID = requestIDRef.current + 1;
      requestIDRef.current = requestID;
      const canHandleRequest = () =>
        isMounted && requestID >= lastHandledRequestIDRef.current;

      try {
        const data = await leaderboardApi.top50(controller.signal);
        if (!canHandleRequest()) {
          return;
        }
        lastHandledRequestIDRef.current = requestID;
        entriesRef.current = data.entries;
        setEntries(data.entries);
        setLoadError(null);
      } catch (error) {
        if (isAbortError(error)) {
          return;
        }
        if (!canHandleRequest()) {
          return;
        }
        lastHandledRequestIDRef.current = requestID;
        if (error instanceof ApiError && error.status === 429) {
          applyBackoff(computeRetryDelayMs(error.retryAfter));
        }
        if (entriesRef.current.length === 0) {
          setLoadError(
            error instanceof ApiError
              ? error.message
              : 'Не удалось загрузить рейтинг',
          );
        } else {
          setLoadError(null);
        }
      } finally {
        if (canHandleRequest()) {
          setLoading(false);
        }
      }
    };

    fetchLeaderboard();
    startInterval();

    return () => {
      isMounted = false;
      controller.abort();
      if (pollInterval !== null) {
        clearInterval(pollInterval);
      }
      if (backoffTimer !== null) {
        clearTimeout(backoffTimer);
      }
    };
  }, []);

  const totalPlayers = entries.length;
  const totalWins = entries.reduce((sum, e) => sum + e.wins, 0);
  const avgTime = totalPlayers > 0
    ? entries.reduce((sum, e) => sum + e.average_solve_time_ms, 0) / totalPlayers
    : 0;

  return (
    <main className={styles.container}>
      <Link href="/" className={styles.homeLink}>На главную</Link>
      <header className={styles.header}>
        <div className={styles.headerCopy}>
          <h1 className={styles.title}>Leaderboard</h1>
          <p className={styles.subtitle}>Рейтинг лучших игроков в CTF дуэлях</p>
        </div>
        <span className={styles.liveStatus}>Live</span>
      </header>
      <dl className={styles.statsRow}>
        <div className={styles.stat}>
          <dt>Всего игроков</dt>
          <dd>{totalPlayers}</dd>
        </div>
        <div className={styles.stat}>
          <dt>Всего побед</dt>
          <dd>{totalWins}</dd>
        </div>
        <div className={styles.stat}>
          <dt>Среднее время</dt>
          <dd>{formatTime(avgTime)}</dd>
        </div>
      </dl>
      <div className={styles.boardWrapper} aria-busy={loading}>
        {loading ? (
          <div className={styles.loading} role="status">
            <div className={styles.spinner} aria-hidden="true" />
            <p>Загрузка рейтинга...</p>
          </div>
        ) : loadError && entries.length === 0 ? (
          <div className={`${styles.empty} ${styles.error}`} role="alert">
            <p>{loadError}</p>
          </div>
        ) : entries.length === 0 ? (
          <div className={styles.empty}>
            <p>Пока нет данных о игроках</p>
          </div>
        ) : (
          <table className={styles.board} aria-label="Рейтинг игроков">
            <colgroup>
              <col className={styles.rankColumn} />
              <col className={styles.playerColumn} />
              <col className={styles.winsColumn} />
              <col className={styles.timeColumn} />
            </colgroup>
            <thead>
              <tr>
                <th scope="col">#</th>
                <th scope="col">Игрок</th>
                <th scope="col" className={styles.numeric}>Победы</th>
                <th scope="col" className={styles.numeric}>Время</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((entry) => (
                <tr key={entry.username} className={entry.rank <= 3 ? styles.leadingRow : undefined}>
                  <td className={styles.rankCell}>#{entry.rank}</td>
                  <th scope="row" className={styles.playerName}>{entry.username}</th>
                  <td className={`${styles.numeric} ${styles.winsCell}`}>{entry.wins}</td>
                  <td className={styles.numeric}>
                    {formatTime(entry.average_solve_time_ms)}
                    <span className={styles.timeLabel}>среднее</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </main>
  );
}
