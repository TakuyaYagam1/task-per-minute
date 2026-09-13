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
};

export const ThemeToggle = ({ storageKey }: ThemeToggleProps) => {
  const [theme, setTheme] = useState<Theme>(DEFAULT_THEME);

  useEffect(() => {
    const storedTheme = readStoredTheme(storageKey);
    const nextTheme = storedTheme ?? readDocumentTheme();

    setTheme(nextTheme);
    applyTheme(nextTheme);
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

  return (
    <div className={styles.toggle} role="group" aria-label="Тема интерфейса">
      <button
        className={styles.button}
        type="button"
        data-theme-choice="dark"
        aria-label="Темная тема"
        aria-pressed={theme === "dark"}
        onClick={() => selectTheme("dark")}
      >
        Темная
      </button>
      <button
        className={styles.button}
        type="button"
        data-theme-choice="light"
        aria-label="Светлая тема"
        aria-pressed={theme === "light"}
        onClick={() => selectTheme("light")}
      >
        Светлая
      </button>
    </div>
  );
};
