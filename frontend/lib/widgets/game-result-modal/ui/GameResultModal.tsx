"use client";

import React from "react";
import { GameState } from "../../../shared/types";
import { Button } from "../../../shared/ui";

interface GameResultModalProps {
  gameState: GameState;
  onReturnHome: () => void;
}

const STYLES = {
  overlay: {
    position: "fixed" as const,
    top: 0,
    left: 0,
    right: 0,
    bottom: 0,
    backgroundColor: "rgba(0, 0, 0, 0.85)",
    display: "flex",
    flexDirection: "column" as const,
    alignItems: "center",
    justifyContent: "center",
    zIndex: 1000,
    padding: "2rem",
  },

  card: {
    backgroundColor: "rgba(112, 206, 206, 0.8)",
    borderRadius: "1rem",
    padding: "2rem",
    maxWidth: "500px",
    width: "100%",
    textAlign: "center" as const,
    boxShadow: "0 8px 32px rgba(0, 0, 0, 0.6)",
    border: "2px solid",
  },
};

// Возвращает конфигурацию для отображения результата игры
const getResultConfig = (gameState: GameState) => {
  switch (gameState) {
    case "won":
      return {
        emoji: "🏆",
        title: "ПОБЕДА!",
        message: "Поздравляем! Вы успешно решили задание!",
        color: "#4CAF50",
      };
    case "lost":
      return {
        emoji: "😢",
        title: "ПОРАЖЕНИЕ",
        message: "Другой игрок раньше ввел правильный флаг.",
        color: "#FF6B6B",
      };
    case "timeup":
      return {
        emoji: "⏰",
        title: "ВРЕМЯ ВЫШЛО!",
        message: "К сожалению, время на выполнение задания истекло.",
        color: "#FF4444",
      };
    default:
      return {
        emoji: "❓",
        title: "ИГРА ЗАВЕРШЕНА",
        message: "Игра завершена.",
        color: "#888",
      };
  }
};

// Модальное окно с результатом игры и кнопкой возврата
export const GameResultModal: React.FC<GameResultModalProps> = ({
  gameState,
  onReturnHome,
}) => {
  const config = getResultConfig(gameState);

  return (
    <div style={STYLES.overlay}>
      <div
        style={{
          ...STYLES.card,
          borderColor: config.color,
        }}
      >
        <div
          style={{
            fontSize: "4rem",
            marginBottom: "1rem",
          }}
        >
          {config.emoji}
        </div>

        <h2
          style={{
            fontSize: "2rem",
            fontWeight: 700,
            marginBottom: "1rem",
            color: config.color,
          }}
        >
          {config.title}
        </h2>

        <p
          style={{
            fontSize: "1.2rem",
            marginBottom: "2rem",
            lineHeight: "1.5",
            color: "#FFF",
          }}
        >
          {config.message}
        </p>

        <Button onClick={onReturnHome} variant="primary" size="large">
          ВЕРНУТЬСЯ НА ГЛАВНУЮ
        </Button>
      </div>
    </div>
  );
};
