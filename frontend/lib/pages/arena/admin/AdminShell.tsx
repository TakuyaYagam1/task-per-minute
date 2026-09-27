"use client";

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";

import {
  adminNavigationEquals,
  adminNavigationSearch,
  DEFAULT_ADMIN_NAVIGATION,
  parseAdminNavigation,
  type AdminNavigation,
  type AdminSection,
  type TournamentAdminView,
} from "./navigation";
import styles from "../admin.module.css";

export interface AdminShellProps {
  navigation: AdminNavigation;
  onNavigate: (navigation: AdminNavigation) => void;
  children: ReactNode;
}

export interface UseAdminNavigationOptions {
  enabled: boolean;
  dirty: boolean;
}

export interface UseAdminNavigationResult {
  navigation: AdminNavigation;
  navigate: (navigation: AdminNavigation) => void;
  setDirty: (dirty: boolean) => void;
}

const NAVIGATION_WARNING =
  "Есть несохраненные изменения. Перейти без сохранения?";

const ADMIN_HISTORY_INDEX = "__adminHistoryIndex";

type AdminHistoryState = Record<string, unknown> & {
  [ADMIN_HISTORY_INDEX]?: number;
};

const adminUrl = (
  navigation: AdminNavigation,
  baseHref = window.location.href,
): string => {
  const url = new URL(baseHref, window.location.origin);
  const query = adminNavigationSearch(navigation);
  const nextParams = new URLSearchParams(query);
  url.searchParams.set("section", nextParams.get("section") || "tournaments");
  url.searchParams.set("view", nextParams.get("view") || "overview");
  if (nextParams.get("tournament")) {
    url.searchParams.set("tournament", nextParams.get("tournament") as string);
  } else {
    url.searchParams.delete("tournament");
  }
  return `${url.pathname}${url.search}${url.hash}`;
};

const stateWithIndex = (state: unknown, index: number): AdminHistoryState => ({
  ...((state && typeof state === "object" ? state : {}) as AdminHistoryState),
  [ADMIN_HISTORY_INDEX]: index,
});

const confirmNavigation = (): boolean =>
  typeof window === "undefined" || window.confirm(NAVIGATION_WARNING);

export const useAdminNavigation = (
  options: UseAdminNavigationOptions,
): UseAdminNavigationResult => {
  const [navigation, setNavigation] = useState<AdminNavigation>(
    DEFAULT_ADMIN_NAVIGATION,
  );
  const navigationRef = useRef(DEFAULT_ADMIN_NAVIGATION);
  const historyIndexRef = useRef(0);
  const currentUrlRef = useRef("");
  const dirtyRef = useRef(options.dirty);
  const enabledRef = useRef(options.enabled);

  useEffect(() => {
    dirtyRef.current = options.dirty;
  }, [options.dirty]);

  useEffect(() => {
    enabledRef.current = options.enabled;
  }, [options.enabled]);

  useEffect(() => {
    if (!options.enabled) {
      return undefined;
    }

    const initialNavigation = parseAdminNavigation(window.location.search);
    const currentState = window.history.state as AdminHistoryState | null;
    const historyIndex =
      typeof currentState?.[ADMIN_HISTORY_INDEX] === "number"
        ? currentState[ADMIN_HISTORY_INDEX]
        : 0;
    const canonicalUrl = adminUrl(initialNavigation);
    if (
      `${window.location.pathname}${window.location.search}` !== canonicalUrl
    ) {
      window.history.replaceState(
        stateWithIndex(window.history.state, historyIndex),
        "",
        canonicalUrl,
      );
    } else if (currentState?.[ADMIN_HISTORY_INDEX] !== historyIndex) {
      window.history.replaceState(
        stateWithIndex(window.history.state, historyIndex),
        "",
        canonicalUrl,
      );
    }
    navigationRef.current = initialNavigation;
    historyIndexRef.current = historyIndex;
    currentUrlRef.current = canonicalUrl;
    setNavigation(initialNavigation);

    const handlePopState = (): void => {
      const nextNavigation = parseAdminNavigation(window.location.search);
      if (adminNavigationEquals(navigationRef.current, nextNavigation)) {
        return;
      }
      const nextState = window.history.state as AdminHistoryState | null;
      const nextIndex = nextState?.[ADMIN_HISTORY_INDEX];
      if (dirtyRef.current && !confirmNavigation()) {
        if (typeof nextIndex === "number") {
          window.history.go(historyIndexRef.current - nextIndex);
        } else {
          window.history.pushState(
            stateWithIndex(window.history.state, historyIndexRef.current),
            "",
            currentUrlRef.current,
          );
        }
        return;
      }
      navigationRef.current = nextNavigation;
      if (typeof nextIndex === "number") {
        historyIndexRef.current = nextIndex;
      }
      currentUrlRef.current = `${window.location.pathname}${window.location.search}${window.location.hash}`;
      dirtyRef.current = false;
      setNavigation(nextNavigation);
    };

    const handleBeforeUnload = (event: BeforeUnloadEvent): void => {
      if (!dirtyRef.current) {
        return;
      }
      event.preventDefault();
      event.returnValue = "";
    };

    window.addEventListener("popstate", handlePopState);
    window.addEventListener("beforeunload", handleBeforeUnload);
    return () => {
      window.removeEventListener("popstate", handlePopState);
      window.removeEventListener("beforeunload", handleBeforeUnload);
    };
  }, [options.enabled]);

  const navigate = useCallback((nextNavigation: AdminNavigation): void => {
    if (!enabledRef.current) {
      return;
    }
    if (adminNavigationEquals(navigationRef.current, nextNavigation)) {
      return;
    }
    if (dirtyRef.current && !confirmNavigation()) {
      return;
    }
    const nextIndex = historyIndexRef.current + 1;
    window.history.pushState(
      stateWithIndex(window.history.state, nextIndex),
      "",
      adminUrl(nextNavigation, currentUrlRef.current || window.location.href),
    );
    navigationRef.current = nextNavigation;
    historyIndexRef.current = nextIndex;
    currentUrlRef.current = `${window.location.pathname}${window.location.search}${window.location.hash}`;
    dirtyRef.current = false;
    setNavigation(nextNavigation);
  }, []);

  const setDirty = useCallback((dirty: boolean): void => {
    dirtyRef.current = dirty;
  }, []);

  return { navigation, navigate, setDirty };
};

const sectionLabels: Record<AdminSection, string> = {
  players: "Игроки",
  tasks: "Задачи",
  tournaments: "Турниры",
  audit: "Журнал",
};

const sectionNavigation = (
  current: AdminNavigation,
  section: AdminSection,
): AdminNavigation => ({
  section,
  tournamentId: current.tournamentId,
  view: section === "audit" ? "audit" : section === "tournaments" ? current.view : "overview",
});

export const tournamentNavigation = (
  tournamentId: string | null,
  view: TournamentAdminView,
): AdminNavigation => ({
  section: "tournaments",
  tournamentId,
  view,
});

export function AdminShell({
  navigation,
  onNavigate,
  children,
}: AdminShellProps) {
  return (
    <main className={`${styles.container} motion-page gpu-optimized`}>
      <header className={styles.shellHeader}>
        <div className={styles.shellIdentity}>
          <p className={styles.shellEyebrow}>Администрирование</p>
          <h1 className={styles.shellTitle}>Панель управления</h1>
          <p className={styles.shellCurrentSection}>
            Текущий раздел: {sectionLabels[navigation.section]}
          </p>
        </div>
      </header>
      <nav className={styles.shellNav} aria-label="Разделы админ-панели">
        {Object.entries(sectionLabels).map(([section, label]) => {
          const typedSection = section as AdminSection;
          const active = navigation.section === typedSection;
          return (
            <button
              key={typedSection}
              type="button"
              className={`${styles.shellNavItem} ${active ? styles.shellNavItemActive : ""}`}
              aria-current={active ? "page" : undefined}
              onClick={() => onNavigate(sectionNavigation(navigation, typedSection))}
            >
              {label}
            </button>
          );
        })}
      </nav>
      <div className={styles.shellContent}>{children}</div>
    </main>
  );
}
