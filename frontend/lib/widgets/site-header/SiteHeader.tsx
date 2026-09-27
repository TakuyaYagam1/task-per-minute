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

  return (
    <header className={styles.header} data-testid="site-header">
      <div className={styles.inner}>
        <Link href="/" className={styles.brand} aria-label="Arena">
          <Image
            src="/task.png"
            alt=""
            width={48}
            height={30}
            className={styles.logo}
          />
          <span>Arena</span>
        </Link>

        <nav className={styles.actions} aria-label="Основная навигация">
          {!isAdminPath ? (
            <Link href="/leaderboard" className={styles.ratingLink}>
              <span className={styles.ratingLabelWide}>Общий рейтинг</span>
              <span className={styles.ratingLabelCompact}>Рейтинг</span>
            </Link>
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
          <ThemeToggle storageKey={themeStorageKey} variant="inline" />
        </nav>
      </div>
    </header>
  );
}

SiteHeader.displayName = "SiteHeader";
