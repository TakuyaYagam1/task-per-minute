'use client';
import React, { useState, useEffect, useMemo, useRef } from 'react';
import Link from 'next/link';
import styles from './leaderboard.module.css';
import { ApiError, leaderboardApi } from '../../lib/shared/api';

interface LeaderboardEntry {
  rank: number;
  username: string;
  wins: number;
  average_solve_time_ms: number;
}

const getMedalEmoji = (rank: number): string | null => {
  switch(rank) {
    case 1: return '🥇';
    case 2: return '🥈';
    case 3: return '🥉';
    default: return null;
  }
};

const getRankBadgeClass = (rank: number): string => {
  switch(rank) {
    case 1: return styles.rankBadgeGold;
    case 2: return styles.rankBadgeSilver;
    case 3: return styles.rankBadgeBronze;
    default: return '';
  }
};

const getAvatarClass = (rank: number): string => {
  switch(rank) {
    case 1: return styles.avatarGold;
    case 2: return styles.avatarSilver;
    case 3: return styles.avatarBronze;
    default: return '';
  }
};

const getInitials = (username: string): string => {
  return username.slice(0, 2).toUpperCase();
};

const formatTime = (ms: number): string => {
  const totalSeconds = ms / 1000;
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = (totalSeconds % 60).toFixed(1);
  if (minutes > 0) {
    return `${minutes}м ${seconds}с`;
  }
  return `${seconds}с`;
};

const getMaxWins = (entries: LeaderboardEntry[]): number => {
  if (entries.length === 0) return 0;
  return Math.max(...entries.map(e => e.wins));
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

  const maxWins = useMemo(() => getMaxWins(entries), [entries]);

  const topThree = entries.slice(0, 3);
  const restEntries = entries.slice(3);
  const totalPlayers = entries.length;
  const totalWins = entries.reduce((sum, e) => sum + e.wins, 0);
  const avgTime = totalPlayers > 0
    ? entries.reduce((sum, e) => sum + e.average_solve_time_ms, 0) / totalPlayers
    : 0;

  return (
    <main className={styles.container}>
      <Link href="/" className={styles.homeLink}>
        <span aria-hidden="true">←</span>
        На главную
      </Link>
      <div className={styles.header}>
        <div className={styles.headerTop}>
          <span className={styles.crown}>👑</span>
          <h1 className={styles.title}>
            Leaderboard
          </h1>
        </div>
        <p className={styles.subtitle}>Рейтинг лучших игроков в CTF дуэлях</p>
        <div className={styles.badges}>
          <span className={`${styles.badge} ${styles.badgeLive}`}>Live</span>
        </div>
      </div>
      <div className={styles.statsRow}>
        <div className={styles.statCard}>
          <div className={styles.statLabel}>Всего игроков</div>
          <div className={`${styles.statValue} ${styles.statValueAccent}`}>
            {totalPlayers}
          </div>
        </div>
        <div className={styles.statCard}>
          <div className={styles.statLabel}>Всего побед</div>
          <div className={styles.statValue}>{totalWins}</div>
        </div>
        <div className={styles.statCard}>
          <div className={styles.statLabel}>Среднее время</div>
          <div className={styles.statValue}>{formatTime(avgTime)}</div>
        </div>
      </div>
      <div className={styles.boardWrapper}>
        {loading ? (
          <div className={styles.loading}>
            <div className={styles.spinner}></div>
            <p>Загрузка рейтинга...</p>
          </div>
        ) : loadError && entries.length === 0 ? (
          <div className={styles.empty}>
            <div className={styles.emptyIcon}>⚠️</div>
            <p className={styles.emptyText}>{loadError}</p>
          </div>
        ) : entries.length === 0 ? (
          <div className={styles.empty}>
            <div className={styles.emptyIcon}>🏆</div>
            <p className={styles.emptyText}>Пока нет данных о игроках</p>
          </div>
        ) : (
          <>
            {topThree.length > 0 && (
              <div className={styles.podiumContainer}>
                {topThree.map((entry) => (
                  <div
                    key={entry.username}
                    className={`${styles.podiumCard} ${styles[`podiumCard_${entry.rank}`]}`}
                  >
                    <div className={styles.podiumInner}>
                      <span className={styles.podiumMedal}>
                        {getMedalEmoji(entry.rank)}
                      </span>
                      <div className={styles.podiumRank}>
                        #{entry.rank} место
                      </div>
                      <div className={styles.podiumName}>
                        {entry.username}
                      </div>
                      <div className={styles.podiumStats}>
                        <div className={styles.podiumStat}>
                          <span className={styles.podiumStatLabel}>Победы</span>
                          <span className={styles.podiumStatValue}>
                            {entry.wins}
                          </span>
                        </div>
                        <div className={styles.podiumStat}>
                          <span className={styles.podiumStatLabel}>Время</span>
                          <span className={styles.podiumStatValue}>
                            {formatTime(entry.average_solve_time_ms)}
                          </span>
                        </div>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
            {restEntries.length > 0 && (
              <div className={styles.board}>
                <div className={styles.boardHeader}>
                  <div className={styles.colRank}>#</div>
                  <div className={styles.colAvatar}></div>
                  <div className={styles.colPlayer}>Игрок</div>
                  <div className={styles.colTasks}>Победы</div>
                  <div className={styles.colTime}>Время</div>
                </div>
                {restEntries.map((entry) => (
                  <div key={entry.username} className={styles.boardRow}>
                    <div className={styles.rankCell}>
                      <span className={`${styles.rankBadge} ${getRankBadgeClass(entry.rank)}`}>
                        #{entry.rank}
                      </span>
                    </div>
                    <div className={styles.avatarCell}>
                      <div className={`${styles.avatar} ${getAvatarClass(entry.rank)}`}>
                        {getInitials(entry.username)}
                      </div>
                    </div>
                    <div className={styles.playerCell}>
                      <span className={styles.playerName}>{entry.username}</span>
                    </div>
                    <div className={styles.tasksCell}>
                      <span className={styles.tasksValue}>{entry.wins}</span>
                      {maxWins > 0 && (
                        <div className={styles.tasksBar}>
                          <div
                            className={styles.tasksBarFill}
                            style={{
                              width: `${(entry.wins / maxWins) * 100}%`,
                            }}
                          />
                        </div>
                      )}
                    </div>
                    <div className={styles.timeCell}>
                      {formatTime(entry.average_solve_time_ms)}
                      <span className={styles.timeLabel}>среднее</span>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </>
        )}
      </div>
    </main>
  );
}
