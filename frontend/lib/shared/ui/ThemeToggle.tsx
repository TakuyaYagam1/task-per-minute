"use client";

import { useEffect, useState } from "react";
import styles from "./ThemeToggle.module.css";

export type Theme = "dark" | "light";

const DEFAULT_THEME: Theme = "dark";

const isTheme = (value: string | undefined | null): value is Theme =>
  value === "dark" || value === "light";

const readStoredTheme = (storageKey: string): Theme | null => {
  if (typeof window === "undefined") {
    return null;
  }

  try {
    const storedTheme = window.localStorage.getItem(storageKey);
    return isTheme(storedTheme) ? storedTheme : null;
  } catch {
    return null;
  }
};

const readDocumentTheme = (): Theme => {
  if (typeof document === "undefined") {
    return DEFAULT_THEME;
  }

  const documentTheme = document.documentElement.dataset.theme;
  return isTheme(documentTheme) ? documentTheme : DEFAULT_THEME;
};

const applyTheme = (theme: Theme) => {
  if (typeof document === "undefined") {
    return;
  }

  document.documentElement.dataset.theme = theme;
  document.documentElement.style.colorScheme = theme;
};

type ThemeToggleProps = {
  storageKey: string;
  variant?: "floating" | "inline" | "menu" | "switch";
};

const MoonIcon = () => (
  <svg
    aria-hidden="true"
    className={styles.icon}
    fill="none"
    viewBox="0 0 24 24"
  >
    <path
      d="M20.4 15.1A8.5 8.5 0 0 1 8.9 3.6a8.5 8.5 0 1 0 11.5 11.5Z"
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth="1.8"
    />
  </svg>
);

const SunIcon = () => (
  <svg
    aria-hidden="true"
    className={styles.icon}
    fill="none"
    viewBox="0 0 24 24"
  >
    <circle cx="12" cy="12" r="3.75" stroke="currentColor" strokeWidth="1.8" />
    <path
      d="M12 2.5v2M12 19.5v2M4.6 4.6 6 6M18 18l1.4 1.4M2.5 12h2M19.5 12h2M4.6 19.4 6 18M18 6l1.4-1.4"
      stroke="currentColor"
      strokeLinecap="round"
      strokeWidth="1.8"
    />
  </svg>
);

export const ThemeToggle = ({ storageKey, variant = "floating" }: ThemeToggleProps) => {
  const [theme, setTheme] = useState<Theme>(DEFAULT_THEME);

  useEffect(() => {
    const observer = new MutationObserver(() => {
      setTheme(readDocumentTheme());
    });
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });

    const storedTheme = readStoredTheme(storageKey);
    const nextTheme = storedTheme ?? readDocumentTheme();

    setTheme(nextTheme);
    applyTheme(nextTheme);

    return () => observer.disconnect();
  }, [storageKey]);

  const selectTheme = (nextTheme: Theme) => {
    setTheme(nextTheme);
    applyTheme(nextTheme);

    try {
      window.localStorage.setItem(storageKey, nextTheme);
    } catch {
      // The DOM update remains effective when browser storage is unavailable.
    }
  };

  if (variant === "switch") {
    return (
      <button
        className={styles.switchButton}
        type="button"
        role="switch"
        aria-label="Светлая тема"
        aria-checked={theme === "light"}
        data-theme-choice={theme}
        title="Светлая тема"
        onClick={() => selectTheme(theme === "light" ? "dark" : "light")}
      >
        <span className={`${styles.switchTrack} ${theme === "light" ? styles.switchLight : ""}`}>
          <span aria-hidden="true" className={`${styles.switchGlyph} ${styles.switchMoon}`}>
            <MoonIcon />
          </span>
          <span aria-hidden="true" className={`${styles.switchGlyph} ${styles.switchSun}`}>
            <SunIcon />
          </span>
          <span aria-hidden="true" className={styles.switchKnob}>
            {theme === "light" ? <SunIcon /> : <MoonIcon />}
          </span>
        </span>
      </button>
    );
  }

  return (
    <div
      className={`${styles.toggle} ${variant === "inline" ? styles.inline : ""} ${variant === "menu" ? styles.menu : ""}`}
      role="group"
      aria-label="Тема интерфейса"
    >
      <button
        className={`${styles.button} ${variant === "menu" ? styles.menuButton : ""}`}
        type="button"
        data-theme-choice="dark"
        aria-label="Темная тема"
        aria-pressed={theme === "dark"}
        title="Темная тема"
        onClick={() => selectTheme("dark")}
      >
        <MoonIcon />
        {variant === "menu" ? <span>Темная</span> : null}
      </button>
      <button
        className={`${styles.button} ${variant === "menu" ? styles.menuButton : ""}`}
        type="button"
        data-theme-choice="light"
        aria-label="Светлая тема"
        aria-pressed={theme === "light"}
        title="Светлая тема"
        onClick={() => selectTheme("light")}
      >
        <SunIcon />
        {variant === "menu" ? <span>Светлая</span> : null}
      </button>
    </div>
  );
};
