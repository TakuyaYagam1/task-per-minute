"use client";

import React, { useCallback, useEffect, useRef } from "react";
import { PacManLoader } from "./PacManLoader";
import { ViewportPortal } from "../../../shared/ui";
import styles from "./WaitingOverlay.module.css";

interface WaitingOverlayProps {
  onCancel: () => void;
  onChangePlayer?: () => void;
  changePlayerDisabled?: boolean;
  queueSize?: number;
  returnFocusRef?: React.RefObject<HTMLButtonElement | null>;
}

export const WaitingOverlay: React.FC<WaitingOverlayProps> = ({
  onCancel,
  onChangePlayer,
  changePlayerDisabled = false,
  queueSize,
  returnFocusRef,
}) => {
  const panelRef = useRef<HTMLDivElement>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);

  const focusPanel = useCallback((panel: HTMLDivElement | null) => {
    panelRef.current = panel;
    if (!panel) return;
    previousFocusRef.current = document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null;
    panel.querySelector<HTMLButtonElement>("button:not(:disabled)")?.focus();
  }, []);

  useEffect(() => {
    const returnFocus = returnFocusRef?.current;
    return () => {
      const target = returnFocus ?? previousFocusRef.current;
      if (target?.isConnected) {
        target.focus();
      }
    };
  }, [returnFocusRef]);

  const keepFocusInPanel = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== "Tab") return;
    const buttons = panelRef.current?.querySelectorAll<HTMLButtonElement>("button:not(:disabled)");
    if (!buttons?.length) return;
    const first = buttons[0];
    const last = buttons[buttons.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  };

  return (
    <ViewportPortal>
      <div className={styles.overlay}>
        <div
          ref={focusPanel}
          className={styles.panel}
          role="dialog"
          aria-modal="true"
          aria-labelledby="waiting-title"
          aria-describedby="waiting-description"
          onKeyDown={keepFocusInPanel}
        >
          <PacManLoader />
          <h2 id="waiting-title">Ожидание второго игрока...</h2>
          <p id="waiting-description" className={styles.description}>
            Ваша игра скоро начнется.
            <br />
            Подготовьтесь к решению задачи!
          </p>
          {queueSize !== undefined && (
            <p className={styles.queue} role="status" aria-live="polite">
              В очереди: <strong>{queueSize}</strong> игроков
            </p>
          )}
          <div className={styles.actions}>
            <button type="button" onClick={onCancel} className="btn btn-secondary">
              Отменить поиск
            </button>
            {onChangePlayer && (
              <button
                type="button"
                onClick={onChangePlayer}
                disabled={changePlayerDisabled}
                className={`btn btn-secondary ${styles.changePlayer}`}
              >
                {changePlayerDisabled ? "Смена игрока..." : "Сменить игрока"}
              </button>
            )}
          </div>
        </div>
      </div>
    </ViewportPortal>
  );
};
