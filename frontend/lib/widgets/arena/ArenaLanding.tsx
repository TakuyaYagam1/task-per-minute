import type { ReactNode } from "react";

import styles from "./arena.module.css";

type ArenaLandingProps = Readonly<{
  children?: ReactNode;
}>;

export const ArenaLanding = ({ children }: ArenaLandingProps) => (
  <div className={styles.landingPanel}>
    {children}
  </div>
);

ArenaLanding.displayName = "ArenaLanding";
