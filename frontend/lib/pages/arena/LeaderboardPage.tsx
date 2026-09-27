'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import Link from 'next/link';

import { ApiError, leaderboardApi } from '../../shared/api';

import styles from './leaderboard.module.css';

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

export default function LeaderboardPage() {
  const [entries, setEntries] = useState<LeaderboardEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [isStale, setIsStale] = useState(false);
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
        return Math.min(
          MAX_BACKOFF_MS,
          Math.max(BASE_INTERVAL_MS, Math.round(seconds * 1000)),
        );
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
        setIsStale(false);
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
          setIsStale(true);
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

  const statistics = useMemo(() => {
    const totalPlayers = entries.length;
    const totalWins = entries.reduce((sum, entry) => sum + entry.wins, 0);
    const avgTime = totalPlayers > 0
      ? entries.reduce((sum, entry) => sum + entry.average_solve_time_ms, 0) / totalPlayers
      : 0;
    return { avgTime, totalPlayers, totalWins };
  }, [entries]);

  return (
    <main className={styles.container}>
      <nav className={styles.pageNav} aria-label="Навигация рейтинга">
        <Link href="/" className={styles.homeLink}>
          <span aria-hidden="true">&lt;-</span>
          На главную
        </Link>
        <Link href="/arena" className={styles.arenaLink}>
          Arena
          <span aria-hidden="true">-&gt;</span>
        </Link>
      </nav>
      <div className={styles.header}>
        <h1 className={styles.title}>Общий рейтинг</h1>
        <p className={styles.subtitle}>Рейтинг лучших игроков в CTF дуэлях</p>
        <div className={styles.badges}>
          <span className={`${styles.badge} ${isStale ? styles.badgeStale : styles.badgeLive}`}>
            {isStale ? 'Данные устарели' : 'Live'}
          </span>
        </div>
      </div>
      <div className={styles.statsRow}>
        <div className={styles.statCard}>
          <div className={styles.statLabel}>Всего игроков</div>
          <div className={`${styles.statValue} ${styles.statValueAccent}`}>
            {statistics.totalPlayers}
          </div>
        </div>
        <div className={styles.statCard}>
          <div className={styles.statLabel}>Всего побед</div>
          <div className={styles.statValue}>{statistics.totalWins}</div>
        </div>
        <div className={styles.statCard}>
          <div className={styles.statLabel}>Среднее время</div>
          <div className={styles.statValue}>{formatTime(statistics.avgTime)}</div>
        </div>
      </div>
      <div className={styles.boardWrapper}>
        {loading ? (
          <div className={styles.loading} role="status" aria-live="polite">
            <div className={styles.spinner} aria-hidden="true" />
            <p>Загрузка рейтинга...</p>
          </div>
        ) : loadError && entries.length === 0 ? (
          <div className={styles.empty} role="alert">
            <div className={styles.emptyIcon} aria-hidden="true">!</div>
            <p className={styles.emptyText}>{loadError}</p>
          </div>
        ) : entries.length === 0 ? (
          <div className={styles.empty}>
            <div className={styles.emptyIcon} aria-hidden="true">-</div>
            <p className={styles.emptyText}>Пока нет данных о игроках</p>
          </div>
        ) : (
          <div
            className={styles.board}
            role="region"
            aria-label="Таблица рейтинга"
            tabIndex={0}
          >
            <table>
              <caption className={styles.tableCaption}>Текущий рейтинг игроков</caption>
              <thead>
                <tr>
                  <th scope="col">Место</th>
                  <th scope="col">Игрок</th>
                  <th scope="col">Победы</th>
                  <th scope="col">Среднее время</th>
                </tr>
              </thead>
              <tbody>
                {entries.map((entry) => (
                  <tr key={entry.username}>
                    <th scope="row">{entry.rank}</th>
                    <td>{entry.username}</td>
                    <td>{entry.wins}</td>
                    <td>{formatTime(entry.average_solve_time_ms)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </main>
  );
}
