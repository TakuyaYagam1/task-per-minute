"use client";

import { useEffect, useState } from "react";

import styles from "./BackToTop.module.css";

const VISIBILITY_THRESHOLD = 400;

export function BackToTop() {
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    const updateVisibility = (): void => {
      setVisible(window.scrollY >= VISIBILITY_THRESHOLD);
    };

    updateVisibility();
    window.addEventListener("scroll", updateVisibility, { passive: true });
    return () => window.removeEventListener("scroll", updateVisibility);
  }, []);

  if (!visible) {
    return null;
  }

  const handleClick = (): void => {
    const prefersReducedMotion = window.matchMedia?.(
      "(prefers-reduced-motion: reduce)",
    ).matches;
    window.scrollTo({
      top: 0,
      behavior: prefersReducedMotion ? "auto" : "smooth",
    });
  };

  return (
    <button
      className={styles.button}
      type="button"
      aria-label="Наверх"
      title="Наверх"
      data-testid="back-to-top"
      onClick={handleClick}
    >
      <svg
        className={styles.icon}
        viewBox="0 0 24 24"
        aria-hidden="true"
        focusable="false"
      >
        <path d="M12 19V5m0 0L6 11m6-6 6 6" />
      </svg>
    </button>
  );
}

BackToTop.displayName = "BackToTop";
