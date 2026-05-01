"use client";

import React from "react";
import Image from "next/image";
import { Timer } from "../../game-timer";

interface GameHeaderProps {
  taskTitle?: string;
  timeLimit: number;
  onTimeUp?: () => void;
}

const STYLES = {
  headerCard: {
    backgroundColor: "rgba(112, 206, 206, 0.8)",
    borderRadius: "1rem",
    padding: "1.5rem",
    boxShadow: "0 8px 32px rgba(0, 0, 0, 0.6)",
    color: "#FFF",
    height: "200px",
    display: "flex",
    flexDirection: "row" as const,
    overflow: "hidden",
  },

  headerImage: {
    width: "300px",
    height: "100%",
    position: "relative" as const,
    flexShrink: 0,
  },

  headerContent: {
    flex: 1,
    padding: "0.75rem",
    display: "flex",
    flexDirection: "column" as const,
    justifyContent: "center",
    alignItems: "center",
    textAlign: "center" as const,
    minWidth: 0,
  },
};

// Заголовок игровой страницы с таймером и информацией о задании
export const GameHeader: React.FC<GameHeaderProps> = ({
  taskTitle = "Web Challenge",
  timeLimit,
  onTimeUp,
}) => {
  return (
    <div className="header-card" style={STYLES.headerCard}>
      <div className="header-image" style={STYLES.headerImage}>
        <Image
          src="/task.png"
          alt="Task Per Minute"
          fill
          style={{
            objectFit: "cover",
            objectPosition: "center",
          }}
          priority
        />
      </div>

      <div className="header-content" style={STYLES.headerContent}>
        <div style={{ marginBottom: "1rem" }}>
          <h1
            style={{
              fontSize: "1.75rem",
              fontWeight: 700,
              margin: "0 0 0.5rem 0",
              lineHeight: "1.4",
            }}
          >
            {taskTitle}
          </h1>
          <p
            style={{
              fontSize: "1.1rem",
              margin: 0,
              opacity: 0.8,
              lineHeight: "1.4",
            }}
          >
            Найдите скрытый флаг на веб-странице
          </p>
        </div>

        <div
          className="timer-container"
          style={{
            display: "flex",
            justifyContent: "center",
          }}
        >
          <Timer initialSeconds={timeLimit} onTimeUp={onTimeUp} />
        </div>
      </div>
    </div>
  );
};
