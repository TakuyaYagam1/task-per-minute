"use client";

import Image from "next/image";
import Link from "next/link";
import { usePathname } from "next/navigation";

import { useRegisteredSiteHeaderAuth } from "../../features/site-header";
import { Button, ThemeToggle } from "../../shared/ui";

import styles from "./site-header.module.css";

const themeStorageKey = "task-per-minute-theme";

export function SiteHeader() {
  const auth = useRegisteredSiteHeaderAuth();
  const pathname = usePathname();
  const isAdminPath = pathname === "/admin" || pathname?.startsWith("/admin/");
  const isAuthenticatedAdmin = isAdminPath && auth?.onLogout !== undefined;

  return (
    <header className={styles.header} data-testid="site-header">
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
