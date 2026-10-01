import type { ReactNode } from "react";
import Link from "next/link";

import styles from "./PlayerAuthPanel.module.css";

type PlayerAuthPanelProps = Readonly<{
  title: string;
  description?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  variant?: "default" | "centered";
}>;

export function PlayerAuthPanel({
  title,
  description,
  children,
  footer,
  variant = "default",
}: PlayerAuthPanelProps) {
  const panelClassName = variant === "centered"
    ? `${styles.panel} ${styles.centered}`
    : styles.panel;

  return (
    <main className={styles.page}>
      <section className={panelClassName} aria-labelledby="player-auth-title">
        <Link className={styles.brand} href="/" aria-label="Task Per Minute - на главную">
          Task Per Minute
        </Link>
        <h1 className={styles.title} id="player-auth-title">{title}</h1>
        {description ? <p className={styles.description}>{description}</p> : null}
        <div className={styles.content}>{children}</div>
        {footer ? <div className={styles.footer}>{footer}</div> : null}
      </section>
    </main>
  );
}
