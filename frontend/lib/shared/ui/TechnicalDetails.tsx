import type { ReactNode } from "react";

import styles from "./TechnicalDetails.module.css";

export const TechnicalDetails = ({
  children,
  summary = "Технические данные",
}: Readonly<{ children: ReactNode; summary?: string }>) => (
  <details className={styles.details}>
    <summary>{summary}</summary>
    <div className={styles.content}>{children}</div>
  </details>
);
