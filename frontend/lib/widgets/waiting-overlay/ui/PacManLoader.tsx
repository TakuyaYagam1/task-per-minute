"use client";

import React from "react";
import styles from "./WaitingOverlay.module.css";

export const PacManLoader: React.FC = () => (
  <div className={styles.loader} aria-hidden="true">
    <span />
    <span />
    <span />
  </div>
);
