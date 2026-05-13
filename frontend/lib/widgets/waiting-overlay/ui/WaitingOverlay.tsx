"use client";

import React from "react";
import { PacManLoader } from "./PacManLoader";
import { Button } from "../../../shared/ui";

interface WaitingOverlayProps {
  onCancel: () => void;
  onChangePlayer?: () => void;
  changePlayerDisabled?: boolean;
  queueSize?: number;
}

const STYLES = {
  overlay: {
    position: "absolute" as const,
    top: 0,
    left: 0,
    right: 0,
    bottom: 0,
    backgroundColor: "rgba(0, 0, 0, 0.85)",
    display: "flex",
    flexDirection: "column" as const,
    alignItems: "center",
    justifyContent: "center",
    zIndex: 100,
    padding: "2rem",
  },

  waitingCard: {
    display: "flex",
    flexDirection: "column" as const,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: "rgba(112, 206, 206, 0.8)",
    borderRadius: "1rem",
    padding: "2rem",
    maxWidth: "700px",
    textAlign: "center" as const,
    boxShadow: "0 8px 32px rgba(0, 0, 0, 0.6)",
  },
};
export const WaitingOverlay: React.FC<WaitingOverlayProps> = ({
  onCancel,
  onChangePlayer,
  changePlayerDisabled = false,
  queueSize,
}) => {
  return (
    <div style={STYLES.overlay}>
      <div style={STYLES.waitingCard}>
        <h2
          style={{
            fontSize: "2rem",
            marginBottom: "1.5rem",
            fontWeight: 700,
            color: "#FFF",
          }}
        >
          Ожидание второго игрока...
        </h2>

        <PacManLoader />

        <p
          style={{
            fontSize: "1.2rem",
            lineHeight: "1.6",
            color: "#FFF",
          }}
        >
          Ваша игра скоро начнется.
          <br />
          Подготовьтесь к решению задачи!
        </p>

        {queueSize !== undefined && (
          <p
            style={{
              fontSize: "1rem",
              marginTop: "1rem",
              color: "rgba(255, 255, 255, 0.8)",
            }}
          >
            В очереди: {queueSize} игроков
          </p>
        )}

        <div style={{
          display: "flex",
          flexWrap: "wrap",
          gap: "0.75rem",
          justifyContent: "center",
          marginTop: "2rem",
        }}>
          <Button onClick={onCancel} variant="secondary">
            Отменить поиск
          </Button>
          {onChangePlayer && (
            <Button
              onClick={onChangePlayer}
              disabled={changePlayerDisabled}
              variant="secondary"
            >
              {changePlayerDisabled ? "Смена игрока..." : "Сменить игрока"}
            </Button>
          )}
        </div>
      </div>
    </div>
  );
};
