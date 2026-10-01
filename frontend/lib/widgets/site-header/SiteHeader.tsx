"use client";

import Image from "next/image";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useCallback, useEffect, useId, useRef, useState } from "react";

import { playerModel } from "../../entities/player";
import {
  usePlayerNotificationsCenter,
  useRegisteredSiteHeaderAuth,
  type PlayerNotificationsState,
} from "../../features/site-header";
import { PLAYER_ACCOUNT_DELETED_EVENT } from "../../shared/api";
import type { PlayerNotification } from "../../shared/api";
import { getSafeArenaPublicReturnPath, getSafeArenaReturnPath } from "../../shared/lib";
import { Button, ThemeToggle } from "../../shared/ui";

import styles from "./site-header.module.css";

const themeStorageKey = "task-per-minute-theme";
type HeaderPopover = "notifications" | "account";

type PopoverProps = Readonly<{
  open: boolean;
  onOpenChange: (open: boolean) => void;
}>;

type PlayerHeaderSession = Readonly<{
  pathname: string | null;
  status: "checking" | "authenticated" | "guest";
}>;

const notificationDate = (value: string): string =>
  new Date(value).toLocaleString("ru-RU", {
    day: "2-digit",
    month: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });

const isAuthRoute = (pathname: string | null): boolean =>
  pathname === "/login" || pathname === "/register" || pathname === "/verify-email";

const isOperatorRoute = (pathname: string | null): boolean =>
  pathname === "/arena/operator" || pathname?.startsWith("/arena/operator/") === true;

const buildAuthHref = (
  route: "/login" | "/register",
  pathname: string | null,
  search: string,
): string => {
  if (!pathname) return route;
  const currentPath = `${pathname}${search}`;
  const safeNext =
    getSafeArenaReturnPath(currentPath, "participant") ??
    getSafeArenaPublicReturnPath(currentPath);
  return safeNext ? `${route}?next=${encodeURIComponent(safeNext)}` : route;
};

function GuestHeaderPreferences({
  showAuthLinks,
  loginHref,
  registerHref,
}: Readonly<{ showAuthLinks: boolean; loginHref: string; registerHref: string }>) {
  return (
    <>
      <div className={styles.guestPreferences}>
        <ThemeToggle storageKey={themeStorageKey} variant="switch" />
        <div className={styles.languageSwitch} role="group" aria-label="Язык">
          <button
            type="button"
            className={styles.languageButton}
            aria-pressed="true"
            disabled
          >
            RU
          </button>
          <button
            type="button"
            className={styles.languageButton}
            aria-pressed="false"
            disabled
          >
            EN
          </button>
        </div>
      </div>
      {showAuthLinks ? (
        <div className={styles.guestAuthLinks}>
          <Link className={`${styles.guestAuthLink} ${styles.guestLoginLink}`} href={loginHref}>
            Войти
          </Link>
          <Link className={`${styles.guestAuthLink} ${styles.guestRegisterLink}`} href={registerHref}>
            Регистрация
          </Link>
        </div>
      ) : null}
    </>
  );
}

function PlayerNotificationBell({
  notifications,
  open,
  onOpenChange,
  suppressToast = false,
}: PopoverProps & Readonly<{ notifications: PlayerNotificationsState; suppressToast?: boolean }>) {
  const rootRef = useRef<HTMLDivElement | null>(null);
  const buttonRef = useRef<HTMLButtonElement | null>(null);
  const panelId = useId();
  const titleId = useId();

  useEffect(() => {
    if (!notifications.visible) {
      onOpenChange(false);
    }
  }, [notifications.visible, onOpenChange]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }

    const handlePointerDown = (event: PointerEvent): void => {
      if (event.target instanceof Node && !rootRef.current?.contains(event.target)) {
        onOpenChange(false);
      }
    };
    const handleKeyDown = (event: KeyboardEvent): void => {
      if (event.key === "Escape") {
        onOpenChange(false);
        buttonRef.current?.focus();
      }
    };
    const handleFocusIn = (event: FocusEvent): void => {
      if (event.target instanceof Node && !rootRef.current?.contains(event.target)) {
        onOpenChange(false);
      }
    };

    document.addEventListener("pointerdown", handlePointerDown);
    document.addEventListener("keydown", handleKeyDown);
    document.addEventListener("focusin", handleFocusIn);
    return () => {
      document.removeEventListener("pointerdown", handlePointerDown);
      document.removeEventListener("keydown", handleKeyDown);
      document.removeEventListener("focusin", handleFocusIn);
    };
  }, [onOpenChange, open]);

  if (!notifications.visible) {
    return null;
  }

  const activeNotifications = notifications.notifications.filter(
    (notification) => Date.parse(notification.expires_at) > Date.now(),
  );
  const count = activeNotifications.length;
  const label = count > 0 ? `Уведомления: ${count}` : "Уведомления";

  return (
    <div className={styles.notificationWrap} ref={rootRef}>
      <button
        ref={buttonRef}
        type="button"
        className={styles.notificationButton}
        aria-label={label}
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        title="Уведомления"
        onClick={() => {
          const nextOpen = !open;
          onOpenChange(nextOpen);
          if (nextOpen) {
            notifications.refresh();
          }
        }}
      >
        <svg
          aria-hidden="true"
          className={styles.notificationIcon}
          fill="none"
          viewBox="0 0 24 24"
        >
          <path
            d="M18 9a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9ZM10 21h4"
            stroke="currentColor"
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth="1.8"
          />
        </svg>
        {count > 0 ? (
          <span className={styles.notificationCount} aria-hidden="true">
            {count > 99 ? "99+" : count}
          </span>
        ) : null}
      </button>

      {open ? (
        <section
          id={panelId}
          className={styles.notificationPanel}
          aria-labelledby={titleId}
          aria-busy={notifications.status === "checking"}
        >
          <h2 className={styles.notificationTitle} id={titleId}>Уведомления</h2>
          <div className={styles.notificationPanelBody}>
            {notifications.status === "checking" ? (
              <p className={styles.notificationMessage} role="status">Загрузка...</p>
            ) : null}
            {notifications.status === "signed_out" ? (
              <p className={styles.notificationMessage} role="status">
                Сессия завершена. Войдите снова.
              </p>
            ) : null}
            {notifications.status === "forbidden" ? (
              <p className={styles.notificationMessage} role="alert">
                Нет доступа к уведомлениям.
              </p>
            ) : null}
            {notifications.status === "error" ? (
              <p className={styles.notificationMessage} role="alert">
                Не удалось обновить уведомления. Повторим автоматически.
              </p>
            ) : null}
            {activeNotifications.length > 0 ? (
              <ol className={styles.notificationList}>
                {activeNotifications.map((notification) => (
                  <NotificationItem key={notification.id} notification={notification} />
                ))}
              </ol>
            ) : notifications.status === "ready" ? (
              <p className={styles.notificationMessage}>
                За последние 24 часа уведомлений нет.
              </p>
            ) : null}
          </div>
          <footer className={styles.notificationFooter}>
            <Link
              className={styles.notificationFooterLink}
              href="/settings?section=notifications"
              onClick={() => onOpenChange(false)}
            >
              Просмотреть все
            </Link>
          </footer>
        </section>
      ) : null}
      {notifications.toast && !open && !suppressToast ? (
        <div className={styles.notificationToast} role="status" aria-live="polite" aria-atomic="true">
          <p>
            Администратор удалил вас из соревнования &quot;{notifications.toast.tournament_name}&quot;.
          </p>
          <time dateTime={notifications.toast.created_at}>
            {notificationDate(notifications.toast.created_at)}
          </time>
        </div>
      ) : null}
    </div>
  );
}

type AvatarSource = Readonly<{ url: string; contentType: string; pathKey: string | null }>;

function PlayerAccountAvatar({
  enabled,
  pathKey,
}: Readonly<{ enabled: boolean; pathKey: string | null }>) {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const activeUrlRef = useRef<string | null>(null);
  const requestRef = useRef<AbortController | null>(null);
  const requestIdRef = useRef(0);
  const [avatar, setAvatar] = useState<AvatarSource | null>(null);
  const [reloadVersion, setReloadVersion] = useState(0);
  const [prefersReducedMotion, setPrefersReducedMotion] = useState(true);

  const clearAvatar = useCallback((): void => {
    requestRef.current?.abort();
    requestRef.current = null;
    requestIdRef.current += 1;
    if (activeUrlRef.current) {
      URL.revokeObjectURL(activeUrlRef.current);
      activeUrlRef.current = null;
    }
    setAvatar(null);
  }, []);

  useEffect(() => {
    const unsubscribe = playerModel.subscribeAvatarChanges((change) => {
      clearAvatar();
      if (change === "refresh") {
        setReloadVersion((current) => current + 1);
      }
    });
    const handleAccountDeleted = (): void => clearAvatar();
    window.addEventListener(PLAYER_ACCOUNT_DELETED_EVENT, handleAccountDeleted);
    return () => {
      unsubscribe();
      window.removeEventListener(PLAYER_ACCOUNT_DELETED_EVENT, handleAccountDeleted);
    };
  }, [clearAvatar]);

  useEffect(() => {
    const mediaQuery = window.matchMedia("(prefers-reduced-motion: reduce)");
    const updatePreference = (): void => setPrefersReducedMotion(mediaQuery.matches);
    updatePreference();
    mediaQuery.addEventListener("change", updatePreference);
    return () => mediaQuery.removeEventListener("change", updatePreference);
  }, []);

  useEffect(() => {
    const video = videoRef.current;
    if (!video || avatar?.contentType !== "video/mp4") return undefined;

    let disposed = false;
    const updatePlayback = (): void => {
      if (prefersReducedMotion || document.hidden) {
        video.pause();
        return;
      }
      void video.play().catch((error: unknown) => {
        if (disposed || videoRef.current !== video) return;
        if (error instanceof DOMException && ["AbortError", "NotAllowedError"].includes(error.name)) return;
        if (activeUrlRef.current === avatar.url) clearAvatar();
      });
    };
    document.addEventListener("visibilitychange", updatePlayback);
    updatePlayback();
    return () => {
      disposed = true;
      document.removeEventListener("visibilitychange", updatePlayback);
      video.pause();
    };
  }, [avatar?.contentType, avatar?.url, clearAvatar, prefersReducedMotion]);

  useEffect(() => {
    clearAvatar();
    if (!enabled) return undefined;

    const requestId = requestIdRef.current + 1;
    requestIdRef.current = requestId;
    const controller = new AbortController();
    requestRef.current = controller;
    let current = true;

    void playerModel.getAvatar(controller.signal).then((result) => {
      if (!current || controller.signal.aborted || requestIdRef.current !== requestId) return;
      if (result.kind !== "ok" || !result.avatar) {
        setAvatar(null);
        return;
      }

      const url = URL.createObjectURL(result.avatar);
      if (!current || controller.signal.aborted || requestIdRef.current !== requestId) {
        URL.revokeObjectURL(url);
        return;
      }
      activeUrlRef.current = url;
      setAvatar({ url, contentType: result.avatar.type.toLowerCase(), pathKey });
    }).catch(() => {
      if (current && !controller.signal.aborted && requestIdRef.current === requestId) {
        setAvatar(null);
      }
    }).finally(() => {
      if (requestRef.current === controller) requestRef.current = null;
    });

    return () => {
      current = false;
      controller.abort();
      if (requestRef.current === controller) requestRef.current = null;
      if (activeUrlRef.current) {
        URL.revokeObjectURL(activeUrlRef.current);
        activeUrlRef.current = null;
      }
      requestIdRef.current += 1;
    };
  }, [clearAvatar, enabled, pathKey, reloadVersion]);

  const handleMediaError = useCallback((url: string): void => {
    if (activeUrlRef.current === url) clearAvatar();
  }, [clearAvatar]);

  if (!enabled || !avatar || avatar.pathKey !== pathKey) {
    return (
      <svg
        aria-hidden="true"
        className={styles.accountIcon}
        fill="none"
        viewBox="0 0 24 24"
      >
        <circle cx="12" cy="8" r="3.25" stroke="currentColor" strokeWidth="1.8" />
        <path
          d="M5 20c.7-3.4 3.2-5.2 7-5.2s6.3 1.8 7 5.2"
          stroke="currentColor"
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="1.8"
        />
      </svg>
    );
  }
  if (avatar.contentType === "video/mp4") {
    return (
      <video
        aria-hidden="true"
        autoPlay={!prefersReducedMotion}
        className={styles.accountAvatarMedia}
        loop={!prefersReducedMotion}
        muted
        playsInline
        ref={videoRef}
        src={avatar.url}
        tabIndex={-1}
        onError={() => handleMediaError(avatar.url)}
      />
    );
  }

  return (
    <Image
      aria-hidden="true"
      className={styles.accountAvatarMedia}
      src={avatar.url}
      alt=""
      width={44}
      height={44}
      unoptimized
      onError={() => handleMediaError(avatar.url)}
    />
  );
}

function AccountMenu({
  open,
  onOpenChange,
  settingsHref,
  onLogout,
  logoutPending,
  showPlayerAvatar,
  pathKey,
}: PopoverProps & Readonly<{
  onLogout?: () => void;
  logoutPending: boolean;
  settingsHref?: string;
  showPlayerAvatar: boolean;
  pathKey: string | null;
}>) {
  const rootRef = useRef<HTMLDivElement | null>(null);
  const panelRef = useRef<HTMLElement | null>(null);
  const buttonRef = useRef<HTMLButtonElement | null>(null);
  const panelId = useId();
  const hadLogoutRef = useRef(Boolean(onLogout));

  useEffect(() => {
    if (hadLogoutRef.current && !onLogout) {
      onOpenChange(false);
    }
    hadLogoutRef.current = Boolean(onLogout);
  }, [onLogout, onOpenChange]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }

    const focusFirstEnabled = (): void => {
      panelRef.current
        ?.querySelector<HTMLElement>(
          'button:not([disabled]), a[href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
        )
        ?.focus();
    };
    const frame = window.requestAnimationFrame(focusFirstEnabled);
    const handlePointerDown = (event: PointerEvent): void => {
      if (event.target instanceof Node && !rootRef.current?.contains(event.target)) {
        onOpenChange(false);
      }
    };
    const handleKeyDown = (event: KeyboardEvent): void => {
      if (event.key === "Escape") {
        onOpenChange(false);
        buttonRef.current?.focus();
      }
    };
    const handleFocusIn = (event: FocusEvent): void => {
      if (event.target instanceof Node && !rootRef.current?.contains(event.target)) {
        onOpenChange(false);
      }
    };

    document.addEventListener("pointerdown", handlePointerDown);
    document.addEventListener("keydown", handleKeyDown);
    document.addEventListener("focusin", handleFocusIn);
    return () => {
      window.cancelAnimationFrame(frame);
      document.removeEventListener("pointerdown", handlePointerDown);
      document.removeEventListener("keydown", handleKeyDown);
      document.removeEventListener("focusin", handleFocusIn);
    };
  }, [onOpenChange, open]);

  const settingsContent = (
    <>
      <svg
        aria-hidden="true"
        className={styles.accountRowIcon}
        fill="none"
        viewBox="0 0 24 24"
      >
        <path
          d="M10.3 3.8h3.4l.5 2a6.8 6.8 0 0 1 1.4.8l2-.6 1.7 2.9-1.5 1.4a7 7 0 0 1 0 1.6l1.5 1.4-1.7 2.9-2-.6a6.8 6.8 0 0 1-1.4.8l-.5 2h-3.4l-.5-2a6.8 6.8 0 0 1-1.4-.8l-2 .6-1.7-2.9 1.5-1.4a7 7 0 0 1 0-1.6L4.7 8.9l1.7-2.9 2 .6a6.8 6.8 0 0 1 1.4-.8l.5-2Z"
          stroke="currentColor"
          strokeLinejoin="round"
          strokeWidth="1.5"
        />
        <circle cx="12" cy="12" r="2.3" stroke="currentColor" strokeWidth="1.5" />
      </svg>
      <span>Настройки</span>
    </>
  );

  return (
    <div className={styles.accountWrap} ref={rootRef}>
      <button
        ref={buttonRef}
        type="button"
        className={styles.accountButton}
        aria-label="Меню аккаунта"
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        title="Меню аккаунта"
        onClick={() => onOpenChange(!open)}
      >
        <PlayerAccountAvatar
          enabled={showPlayerAvatar && !logoutPending}
          pathKey={pathKey}
        />
      </button>

      {open ? (
        <section
          id={panelId}
          ref={panelRef}
          className={styles.accountPanel}
          aria-label="Аккаунт"
        >
          {settingsHref ? (
            <Link
              href={settingsHref}
              className={styles.accountSettingsButton}
              aria-label="Настройки"
              title="Настройки"
              onClick={() => onOpenChange(false)}
            >
              {settingsContent}
            </Link>
          ) : null}
          <button
            type="button"
            className={styles.accountLanguageButton}
            title="Выбор языка пока недоступен"
            disabled
          >
            <span className={styles.accountLanguageLabel}>
              <svg
                aria-hidden="true"
                className={styles.accountRowIcon}
                fill="none"
                viewBox="0 0 24 24"
              >
                <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="1.7" />
                <path
                  d="M3.5 12h17M12 3c2.3 2.5 3.3 5.5 3.3 9s-1 6.5-3.3 9c-2.3-2.5-3.3-5.5-3.3-9S9.7 5.5 12 3Z"
                  stroke="currentColor"
                  strokeLinejoin="round"
                  strokeWidth="1.5"
                />
              </svg>
              <span>Язык</span>
            </span>
            <span className={styles.accountLanguageValue}>Русский</span>
          </button>
          <div className={styles.accountThemeRow}>
            <span className={styles.accountThemeLabel}>Тема</span>
            <ThemeToggle storageKey={themeStorageKey} variant="menu" />
          </div>
          {onLogout ? (
            <div className={styles.accountLogoutSection}>
              <Button
                type="button"
                variant="ghost"
                loading={logoutPending}
                loadingLabel="Выход..."
                className={styles.accountLogoutButton}
                onClick={onLogout}
              >
                <svg
                  aria-hidden="true"
                  className={styles.accountRowIcon}
                  fill="none"
                  viewBox="0 0 24 24"
                >
                  <path
                    d="M10 4H5.5A1.5 1.5 0 0 0 4 5.5v13A1.5 1.5 0 0 0 5.5 20H10M14 8l4 4-4 4M8 12h10"
                    stroke="currentColor"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    strokeWidth="1.8"
                  />
                </svg>
                Выйти
              </Button>
            </div>
          ) : null}
        </section>
      ) : null}
    </div>
  );
}

function NotificationItem({ notification }: Readonly<{ notification: PlayerNotification }>) {
  return (
    <li className={styles.notificationItem}>
      <p>
        Администратор удалил вас из соревнования &quot;{notification.tournament_name}&quot;.
      </p>
      <time dateTime={notification.created_at}>
        {notificationDate(notification.created_at)}
      </time>
    </li>
  );
}

export function SiteHeader() {
  const auth = useRegisteredSiteHeaderAuth();
  const notificationCenter = usePlayerNotificationsCenter();
  const setNotificationsEnabled = notificationCenter.setEnabled;
  const notificationStatus = notificationCenter.status;
  const pathname = usePathname();
  const headerRef = useRef<HTMLElement | null>(null);
  const playerSessionRequestRef = useRef<AbortController | null>(null);
  const [routeSearch, setRouteSearch] = useState<Readonly<{ pathname: string | null; search: string }>>({
    pathname: null,
    search: "",
  });
  const [playerSession, setPlayerSession] = useState<PlayerHeaderSession>({
    pathname: null,
    status: "guest",
  });
  const playerSessionRef = useRef(playerSession);
  playerSessionRef.current = playerSession;
  const [fallbackLogoutPending, setFallbackLogoutPending] = useState(false);
  const [openPopover, setOpenPopover] = useState<HeaderPopover | null>(null);
  const isAdminPath = pathname === "/admin" || pathname?.startsWith("/admin/");
  const isOperatorPath = isOperatorRoute(pathname);
  const isAdministrativeRoute = isAdminPath || isOperatorPath;
  const isSettingsRoute = pathname === "/settings" || pathname?.startsWith("/settings/") === true;
  const isAuthenticationRoute = isAuthRoute(pathname);
  const shouldVerifyPlayer =
    Boolean(pathname) &&
    pathname !== "/" &&
    !isAdministrativeRoute &&
    !isAuthenticationRoute;
  const isAuthenticatedAdmin = isAdministrativeRoute && auth?.onLogout !== undefined;
  const isVerifiedPlayer =
    shouldVerifyPlayer &&
    playerSession.pathname === pathname &&
    playerSession.status === "authenticated";
  const showAccountMenu = isAuthenticatedAdmin || isVerifiedPlayer;
  const showGuestAuthLinks =
    !isAuthenticationRoute && !isAdministrativeRoute && !isVerifiedPlayer;
  const isPlayerLogoutPending = auth?.logoutPending === true || fallbackLogoutPending;
  const currentSearch = routeSearch.pathname === pathname ? routeSearch.search : "";
  const headerLoginHref = buildAuthHref("/login", pathname, currentSearch);
  const headerRegisterHref = buildAuthHref("/register", pathname, currentSearch);
  const checkPlayerSession = useCallback((targetPath: string | null): AbortController => {
    playerSessionRequestRef.current?.abort();
    const controller = new AbortController();
    playerSessionRequestRef.current = controller;
    setPlayerSession({ pathname: targetPath, status: "checking" });
    void playerModel.restoreCurrentPlayer(controller.signal).then((result) => {
      if (controller.signal.aborted || playerSessionRequestRef.current !== controller) return;
      if (result.kind === "aborted") return;
      setPlayerSession({
        pathname: targetPath,
        status: result.kind === "ok" ? "authenticated" : "guest",
      });
    }).catch(() => {
      if (!controller.signal.aborted && playerSessionRequestRef.current === controller) {
        setPlayerSession({ pathname: targetPath, status: "guest" });
      }
    }).finally(() => {
      if (playerSessionRequestRef.current === controller) {
        playerSessionRequestRef.current = null;
      }
    });
    return controller;
  }, []);
  const setNotificationsOpen = useCallback((open: boolean): void => {
    setOpenPopover((current) =>
      open ? "notifications" : current === "notifications" ? null : current,
    );
  }, []);
  const setAccountOpen = useCallback((open: boolean): void => {
    setOpenPopover((current) =>
      open ? "account" : current === "account" ? null : current,
    );
  }, []);

  useEffect(() => {
    setOpenPopover(null);
  }, [pathname]);

  useEffect(() => {
    setRouteSearch({ pathname, search: window.location.search });
  }, [pathname]);

  useEffect(() => {
    setNotificationsEnabled(isVerifiedPlayer && !isPlayerLogoutPending);
  }, [isPlayerLogoutPending, isVerifiedPlayer, setNotificationsEnabled]);

  useEffect(() => {
    if (!isVerifiedPlayer || notificationStatus !== "signed_out") return;
    setNotificationsEnabled(false);
    playerSessionRequestRef.current?.abort();
    playerSessionRequestRef.current = null;
    setPlayerSession({ pathname, status: "guest" });
    void playerModel.clearCurrentPlayer();
  }, [isVerifiedPlayer, notificationStatus, pathname, setNotificationsEnabled]);

  useEffect(() => {
    if (!shouldVerifyPlayer) {
      playerSessionRequestRef.current?.abort();
      playerSessionRequestRef.current = null;
      setPlayerSession({ pathname, status: "guest" });
      return undefined;
    }

    checkPlayerSession(pathname);
    return () => {
      const activeRequest = playerSessionRequestRef.current;
      activeRequest?.abort();
      playerSessionRequestRef.current = null;
    };
  }, [checkPlayerSession, pathname, shouldVerifyPlayer]);

  useEffect(() => {
    const handlePlayerSessionChange = (change: "refresh" | "clear"): void => {
      if (change === "clear") {
        setNotificationsEnabled(false);
        playerSessionRequestRef.current?.abort();
        playerSessionRequestRef.current = null;
        setPlayerSession({ pathname, status: "guest" });
        return;
      }
      if (shouldVerifyPlayer && playerSessionRef.current.status !== "authenticated") {
        checkPlayerSession(pathname);
      }
    };
    const unsubscribe = playerModel.subscribeAvatarChanges(handlePlayerSessionChange);
    const handleAccountDeleted = (): void => handlePlayerSessionChange("clear");
    window.addEventListener(PLAYER_ACCOUNT_DELETED_EVENT, handleAccountDeleted);
    return () => {
      unsubscribe();
      window.removeEventListener(PLAYER_ACCOUNT_DELETED_EVENT, handleAccountDeleted);
    };
  }, [checkPlayerSession, pathname, setNotificationsEnabled, shouldVerifyPlayer]);

  const handleFallbackLogout = useCallback(async (): Promise<void> => {
    if (fallbackLogoutPending) return;
    setFallbackLogoutPending(true);
    try {
      await playerModel.clearCurrentPlayer();
    } finally {
      setPlayerSession({ pathname, status: "guest" });
      setFallbackLogoutPending(false);
    }
  }, [fallbackLogoutPending, pathname]);

  useEffect(() => {
    const header = headerRef.current;
    if (!header) {
      return;
    }

    const rootStyle = document.documentElement.style;
    const previousHeight = rootStyle.getPropertyValue("--site-header-height");
    const updateHeight = () => {
      rootStyle.setProperty(
        "--site-header-height",
        `${header.getBoundingClientRect().height}px`,
      );
    };

    updateHeight();
    const observer = typeof ResizeObserver === "undefined"
      ? null
      : new ResizeObserver(updateHeight);
    observer?.observe(header);

    return () => {
      observer?.disconnect();
      if (previousHeight) {
        rootStyle.setProperty("--site-header-height", previousHeight);
      } else {
        rootStyle.removeProperty("--site-header-height");
      }
    };
  }, []);

  const menuLogout = isAuthenticatedAdmin || isVerifiedPlayer
    ? auth?.onLogout ?? (isVerifiedPlayer ? handleFallbackLogout : undefined)
    : undefined;
  const menuLogoutPending = auth?.onLogout
    ? auth.logoutPending
    : isVerifiedPlayer && fallbackLogoutPending;

  return (
    <header ref={headerRef} className={styles.header} data-testid="site-header">
      <div className={`${styles.inner} ${isAdministrativeRoute ? styles.adminInner : ""} ${isSettingsRoute ? styles.settingsInner : ""} ${showAccountMenu ? "" : styles.guestHeaderInner}`}>
        <Link
          href={isAdminPath ? "/" : "/arena"}
          className={`${styles.brand} ${isAdministrativeRoute ? styles.adminBrand : ""}`}
          aria-label={isAdminPath ? "Панель управления - на главную" : "Arena"}
        >
          <Image
            src="/task.png"
            alt=""
            width={48}
            height={30}
            className={styles.logo}
          />
          {isAdminPath ? (
            isAuthenticatedAdmin ? (
              <h1 className={styles.adminTitle}>Панель управления</h1>
            ) : (
              <span className={styles.adminTitle}>Панель управления</span>
            )
          ) : (
            <span>Arena</span>
          )}
        </Link>

        <nav
          className={`${styles.actions} ${isAdministrativeRoute ? styles.adminActions : ""} ${showAccountMenu ? "" : styles.guestHeaderActions}`}
          aria-label={isAdministrativeRoute ? "Управление сессией" : isAuthenticationRoute ? "Настройки отображения" : "Основная навигация"}
        >
          {!isAdministrativeRoute || isAuthenticatedAdmin ? (
            <Link
              href="/leaderboard"
              className={`${styles.ratingLink} ${showGuestAuthLinks ? styles.guestRatingLink : ""}`}
            >
              <span className={styles.ratingLabelWide}>Общий рейтинг</span>
              <span className={styles.ratingLabelCompact}>Рейтинг</span>
            </Link>
          ) : null}
          {isVerifiedPlayer && !isPlayerLogoutPending ? (
            <span aria-hidden="true" className={styles.ratingDivider} />
          ) : null}
          {isVerifiedPlayer && !isPlayerLogoutPending ? (
            <PlayerNotificationBell
              notifications={notificationCenter}
              open={openPopover === "notifications"}
              suppressToast={openPopover === "account"}
              onOpenChange={setNotificationsOpen}
            />
          ) : null}
          {showAccountMenu ? (
            <AccountMenu
              open={openPopover === "account"}
              onOpenChange={setAccountOpen}
              settingsHref={isAdministrativeRoute ? undefined : "/settings"}
              onLogout={menuLogout}
              logoutPending={menuLogoutPending}
              showPlayerAvatar={isVerifiedPlayer}
              pathKey={pathname}
            />
          ) : (
            <GuestHeaderPreferences
              showAuthLinks={showGuestAuthLinks}
              loginHref={headerLoginHref}
              registerHref={headerRegisterHref}
            />
          )}
        </nav>
      </div>
    </header>
  );
}

SiteHeader.displayName = "SiteHeader";
