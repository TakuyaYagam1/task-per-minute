import type { ReactNode } from "react";
import Link from "next/link";

import styles from "./PlayerAuthPanel.module.css";

type PlayerAuthPanelProps = Readonly<{
  title: string;
  description: string;
  children: ReactNode;
  footer: ReactNode;
}>;

export function PlayerAuthPanel({
  title,
  description,
  children,
  footer,
}: PlayerAuthPanelProps) {
  return (
    <main className={styles.page}>
      <section className={styles.panel} aria-labelledby="player-auth-title">
        <Link className={styles.brand} href="/" aria-label="Task Per Minute - на главную">
          Task Per Minute
        </Link>
        <h1 className={styles.title} id="player-auth-title">{title}</h1>
        <p className={styles.description}>{description}</p>
        <div className={styles.content}>{children}</div>
        <div className={styles.footer}>{footer}</div>
      </section>
    </main>
  );
}
