"use client";

import React, { useState, useEffect } from "react";
import { formatTime } from "../../../shared/lib";

interface TimerProps {
  initialSeconds: number;
  onTimeUp?: () => void;
}

// Компонент таймера обратного отсчета с визуальными эффектами
export const Timer: React.FC<TimerProps> = ({ initialSeconds, onTimeUp }) => {
  const [timeLeft, setTimeLeft] = useState(initialSeconds);

  useEffect(() => {
    setTimeLeft(initialSeconds);

    if (initialSeconds <= 0) {
      onTimeUp?.();
      return;
    }

    const interval = setInterval(() => {
      setTimeLeft((prev) => {
        const newTime = prev - 1;
        if (newTime <= 0) {
          onTimeUp?.();
          return 0;
        }
        return newTime;
      });
    }, 1000);

    return () => clearInterval(interval);
  }, [initialSeconds, onTimeUp]);

  // Возвращает стили для таймера в зависимости от оставшегося времени
  const getTimerStyle = () => ({
    backgroundColor:
      timeLeft <= 10 ? "#FF2222" : timeLeft <= 60 ? "#FF4444" : "#FF6B6B",
    color: "#FFF",
    padding: "0.75rem 1.5rem",
    borderRadius: "0.5rem",
    fontSize: "1.75rem",
    fontWeight: 700,
    fontFamily: "monospace",
    minWidth: "120px",
    textAlign: "center" as const,
    boxShadow:
      timeLeft <= 10
        ? "0 4px 15px rgba(255, 34, 34, 0.5)"
        : "0 4px 15px rgba(255, 68, 68, 0.3)",
    animation: timeLeft <= 10 ? "pulse 1s infinite" : "none",
  });

  return <div style={getTimerStyle()}>{formatTime(timeLeft)}</div>;
};
