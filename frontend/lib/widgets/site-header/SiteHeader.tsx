"use client";

import Image from "next/image";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useRef } from "react";

import { useRegisteredSiteHeaderAuth } from "../../features/site-header";
import { Button, ThemeToggle } from "../../shared/ui";

import styles from "./site-header.module.css";

const themeStorageKey = "task-per-minute-theme";

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
            <ThemeToggle storageKey={themeStorageKey} variant="inline" />
          ) : null}
        </nav>
      </div>
    </header>
  );
}

SiteHeader.displayName = "SiteHeader";
