"use client";

import Image from "next/image";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useId, useRef, useState } from "react";

import {
  usePlayerNotifications,
  useRegisteredSiteHeaderAuth,
} from "../../features/site-header";
import { Button, ThemeToggle } from "../../shared/ui";
import type { PlayerNotification } from "../../shared/api";

import styles from "./site-header.module.css";

const themeStorageKey = "task-per-minute-theme";

const notificationDate = (value: string): string =>
  new Date(value).toLocaleString("ru-RU", {
    day: "2-digit",
    month: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });

function PlayerNotificationBell({ enabled }: Readonly<{ enabled: boolean }>) {
  const notifications = usePlayerNotifications(enabled);
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const buttonRef = useRef<HTMLButtonElement | null>(null);
  const panelId = useId();
  const titleId = useId();

  useEffect(() => {
    if (!notifications.visible) {
      setOpen(false);
    }
  }, [notifications.visible]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }

    const handlePointerDown = (event: PointerEvent): void => {
      if (event.target instanceof Node && !rootRef.current?.contains(event.target)) {
        setOpen(false);
      }
    };
    const handleKeyDown = (event: KeyboardEvent): void => {
      if (event.key === "Escape") {
        setOpen(false);
        buttonRef.current?.focus();
      }
    };

    document.addEventListener("pointerdown", handlePointerDown);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("pointerdown", handlePointerDown);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [open]);

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
          setOpen(nextOpen);
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
          {notifications.status === "checking" ? (
            <p className={styles.notificationMessage} role="status">Загрузка...</p>
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
              Уведомлений за последние 30 минут нет.
            </p>
          ) : null}
        </section>
      ) : null}
      {notifications.toast && !open ? (
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
  const pathname = usePathname();
  const headerRef = useRef<HTMLElement | null>(null);
  const isAdminPath = pathname === "/admin" || pathname?.startsWith("/admin/");
  const isAuthenticatedAdmin = isAdminPath && auth?.onLogout !== undefined;

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

  return (
    <header ref={headerRef} className={styles.header} data-testid="site-header">
      <div className={`${styles.inner} ${isAdminPath ? styles.adminInner : ""}`}>
        <Link
          href="/"
          className={`${styles.brand} ${isAdminPath ? styles.adminBrand : ""}`}
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
          className={`${styles.actions} ${isAdminPath ? styles.adminActions : ""}`}
          aria-label={isAdminPath ? "Управление сессией" : "Основная навигация"}
        >
          {!isAdminPath ? (
            <Link href="/leaderboard" className={styles.ratingLink}>
              <span className={styles.ratingLabelWide}>Общий рейтинг</span>
              <span className={styles.ratingLabelCompact}>Рейтинг</span>
            </Link>
          ) : null}
          {isAdminPath ? (
            <ThemeToggle storageKey={themeStorageKey} variant="inline" />
          ) : null}
          {auth?.onLogout && (
            <Button
              type="button"
              variant="ghost"
              size="small"
              loading={auth.logoutPending}
              loadingLabel="Выход..."
              onClick={auth.onLogout}
            >
              Выйти
            </Button>
          )}
          {!isAdminPath ? (
            <>
              <PlayerNotificationBell enabled={!isAdminPath} />
              <ThemeToggle storageKey={themeStorageKey} variant="inline" />
            </>
          ) : null}
        </nav>
      </div>
    </header>
  );
}

SiteHeader.displayName = "SiteHeader";
