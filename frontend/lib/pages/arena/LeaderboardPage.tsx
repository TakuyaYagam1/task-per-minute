'use client';

import { useEffect, useMemo, useRef, useState } from 'react';

import { ApiError, leaderboardApi } from '../../shared/api';
import type { LeaderboardEntry, LeaderboardWinsFilter } from '../../shared/api';

import LeaderboardAvatar from './LeaderboardAvatar';
import styles from './leaderboard.module.css';

interface LeaderboardQueryState {
  search: string;
  wins: LeaderboardWinsFilter;
  page: number;
}

const DEFAULT_QUERY: LeaderboardQueryState = {
  search: '',
  wins: 'all',
  page: 1,
};

const WINS_FILTERS: readonly LeaderboardWinsFilter[] = [
  'all',
  'withwins',
  'withoutwins',
];

const PAGE_SIZE = 100;
const MAX_SEARCH_LENGTH = 50;

const formatTime = (ms: number): string => {
  const totalSeconds = ms / 1000;
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = (totalSeconds % 60).toFixed(1);
  if (minutes > 0) {
    return `${minutes}м ${seconds}с`;
  }
  return `${seconds}с`;
};

const queryFromLocation = (): LeaderboardQueryState => {
  const params = new URLSearchParams(window.location.search);
  const page = Number(params.get('page'));
  const wins = params.get('wins');
  return {
    search: (params.get('search') ?? '').slice(0, MAX_SEARCH_LENGTH),
    wins: WINS_FILTERS.includes(wins as LeaderboardWinsFilter)
      ? (wins as LeaderboardWinsFilter)
      : 'all',
    page: Number.isSafeInteger(page) && page > 0 ? page : 1,
  };
};

const searchForQuery = (query: LeaderboardQueryState): string => {
  const params = new URLSearchParams();
  if (query.search) {
    params.set('search', query.search);
  }
  if (query.wins !== 'all') {
    params.set('wins', query.wins);
  }
  if (query.page > 1) {
    params.set('page', String(query.page));
  }
  const value = params.toString();
  return value ? `?${value}` : '';
};

export default function LeaderboardPage() {
  const [entries, setEntries] = useState<LeaderboardEntry[]>([]);
  const [query, setQuery] = useState<LeaderboardQueryState>(DEFAULT_QUERY);
  const [searchInput, setSearchInput] = useState('');
  const [queryReady, setQueryReady] = useState(false);
  const [page, setPage] = useState(1);
  const [totalPlayers, setTotalPlayers] = useState(0);
  const [totalPages, setTotalPages] = useState(0);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const entriesRef = useRef<LeaderboardEntry[]>([]);
  const queryRef = useRef(query);
  const requestIDRef = useRef(0);
  const lastHandledRequestIDRef = useRef(0);

  useEffect(() => {
    const syncQuery = () => {
      const nextQuery = queryFromLocation();
      queryRef.current = nextQuery;
      setQuery(nextQuery);
      setSearchInput(nextQuery.search);
      setQueryReady(true);
    };
    syncQuery();
    window.addEventListener('popstate', syncQuery);
    return () => window.removeEventListener('popstate', syncQuery);
  }, []);

  const changeQuery = (nextQuery: LeaderboardQueryState, historyMethod: 'pushState' | 'replaceState') => {
    queryRef.current = nextQuery;
    setQuery(nextQuery);
    window.history[historyMethod](
      window.history.state,
      '',
      `${window.location.pathname}${searchForQuery(nextQuery)}`,
    );
  };

  const handleSearchChange = (value: string) => {
    const search = value.slice(0, MAX_SEARCH_LENGTH);
    setSearchInput(search);
    if (search === queryRef.current.search) {
      return;
    }
    changeQuery({ ...queryRef.current, search, page: 1 }, 'replaceState');
  };

  const handleWinsChange = (wins: LeaderboardWinsFilter) => {
    if (wins === queryRef.current.wins) {
      return;
    }
    changeQuery({ ...queryRef.current, wins, page: 1 }, 'pushState');
  };

  const handlePageChange = (nextPage: number) => {
    if (nextPage < 1 || nextPage === queryRef.current.page) {
      return;
    }
    changeQuery({ ...queryRef.current, page: nextPage }, 'pushState');
  };

  useEffect(() => {
    if (!queryReady) {
      return;
    }

    let isMounted = true;
    const controller = new AbortController();
    let pollInterval: ReturnType<typeof setInterval> | null = null;
    let backoffTimer: ReturnType<typeof setTimeout> | null = null;
    let initialRequestTimer: ReturnType<typeof setTimeout> | null = null;

    const BASE_INTERVAL_MS = 5_000;
    const MAX_BACKOFF_MS = 60_000;

    entriesRef.current = [];
    setEntries([]);
    setTotalPlayers(0);
    setTotalPages(0);
    setLoading(true);
    setLoadError(null);

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
        void fetchLeaderboard();
        startInterval();
      }, delayMs);
    };

    const fetchLeaderboard = async () => {
      const requestID = requestIDRef.current + 1;
      requestIDRef.current = requestID;
      const canHandleRequest = () =>
        isMounted && requestID >= lastHandledRequestIDRef.current;

      try {
        const data = await leaderboardApi.page(
          {
            search: query.search,
            wins: query.wins,
            page: query.page,
          },
          controller.signal,
        );
        if (!canHandleRequest()) {
          return;
        }
        lastHandledRequestIDRef.current = requestID;
        entriesRef.current = data.entries;
        setEntries(data.entries);
        setPage(data.page);
        setTotalPlayers(data.total);
        setTotalPages(data.total_pages);
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

    initialRequestTimer = setTimeout(() => {
      void fetchLeaderboard();
      startInterval();
    }, query.search ? 250 : 0);

    return () => {
      isMounted = false;
      controller.abort();
      if (initialRequestTimer !== null) {
        clearTimeout(initialRequestTimer);
      }
      if (pollInterval !== null) {
        clearInterval(pollInterval);
      }
      if (backoffTimer !== null) {
        clearTimeout(backoffTimer);
      }
    };
  }, [query.page, query.search, query.wins, queryReady]);

  const statistics = useMemo(() => {
    const totalWins = entries.reduce((sum, entry) => sum + entry.wins, 0);
    const avgTime = entries.length > 0
      ? entries.reduce((sum, entry) => sum + entry.average_solve_time_ms, 0) / entries.length
      : 0;
    return { avgTime, totalWins };
  }, [entries]);

  const noResultsMessage = totalPlayers > 0
    ? 'На этой странице нет игроков'
    : query.search || query.wins !== 'all'
      ? 'Игроки не найдены'
      : 'Пока нет данных о игроках';

  return (
    <main className={styles.container}>
      <div className={styles.header}>
        <h1 className={styles.title}>Общий рейтинг</h1>
        <p className={styles.subtitle}>Рейтинг лучших игроков в CTF дуэлях</p>
      </div>
      <div className={styles.filters}>
        <label className={styles.filterField} htmlFor="leaderboard-search">
          Поиск по имени
          <input
            id="leaderboard-search"
            className={styles.filterControl}
            type="search"
            autoComplete="off"
            maxLength={MAX_SEARCH_LENGTH}
            value={searchInput}
            onChange={(event) => handleSearchChange(event.currentTarget.value)}
          />
        </label>
        <label className={styles.filterField} htmlFor="leaderboard-wins">
          Победы
          <select
            id="leaderboard-wins"
            className={styles.filterControl}
            value={query.wins}
            onChange={(event) => handleWinsChange(event.currentTarget.value as LeaderboardWinsFilter)}
          >
            <option value="all">Все игроки</option>
            <option value="withwins">С победами</option>
            <option value="withoutwins">Без побед</option>
          </select>
        </label>
      </div>
      <div className={styles.statsRow}>
        <div className={styles.statCard}>
          <div className={styles.statLabel}>Найдено игроков</div>
          <div className={`${styles.statValue} ${styles.statValueAccent}`}>
            {totalPlayers}
          </div>
        </div>
        <div className={styles.statCard}>
          <div className={styles.statLabel}>Побед на странице</div>
          <div className={styles.statValue}>{statistics.totalWins}</div>
        </div>
        <div className={styles.statCard}>
          <div className={styles.statLabel}>Среднее время на странице</div>
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
            <p className={styles.emptyText}>{noResultsMessage}</p>
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
                  <tr key={entry.avatar?.player_id ?? entry.username}>
                    <th scope="row">{entry.rank}</th>
                    <td>
                      <div className={styles.playerCell}>
                        <LeaderboardAvatar avatar={entry.avatar} />
                        <span>{entry.username}</span>
                      </div>
                    </td>
                    <td>{entry.wins}</td>
                    <td>{formatTime(entry.average_solve_time_ms)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
      <nav className={styles.pagination} aria-label="Страницы рейтинга">
        <button
          className={styles.paginationButton}
          type="button"
          disabled={loading || query.page <= 1}
          onClick={() => handlePageChange(query.page - 1)}
        >
          Предыдущая
        </button>
        <span className={styles.pageSummary} aria-live="polite">
          {totalPages > 0 ? `Страница ${page} из ${totalPages}` : 'Нет страниц'}
        </span>
        <button
          className={styles.paginationButton}
          type="button"
          disabled={loading || query.page >= totalPages}
          onClick={() => handlePageChange(query.page + 1)}
        >
          Следующая
        </button>
      </nav>
    </main>
  );
}
