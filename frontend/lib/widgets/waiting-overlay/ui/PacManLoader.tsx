"use client";

import React from "react";

// Анимированный загрузчик в стиле Pac-Man
export const PacManLoader: React.FC = () => {
  const pacmanSize = 50;
  const primaryColor = "#fed75a";

  return (
    <div
      style={{
        display: "flex",
        justifyContent: "center",
        marginBottom: "1.5rem",
        position: "relative",
        height: `${pacmanSize}px`,
      }}
    >
      <div
        style={{
          position: "relative",
          width: `${pacmanSize}px`,
          height: `${pacmanSize}px`,
        }}
      >
        <div
          className="pac-man-top"
          style={{
            width: `${pacmanSize}px`,
            height: `${pacmanSize / 2}px`,
            background: primaryColor,
            borderRadius: "100em 100em 0 0",
            transformOrigin: "bottom",
            animation: "eating-top 0.5s infinite",
            position: "absolute",
          }}
        />
        <div
          className="pac-man-bottom"
          style={{
            width: `${pacmanSize}px`,
            height: `${pacmanSize / 2}px`,
            background: primaryColor,
            borderRadius: "0 0 100em 100em",
            transformOrigin: "top",
            animation: "eating-bottom 0.5s infinite",
            position: "absolute",
            top: `${pacmanSize / 2}px`,
          }}
        />
        <div
          className="pac-man-ball1"
          style={{
            position: "absolute",
            borderRadius: "100em",
            width: "8px",
            height: "8px",
            backgroundColor: "#fed75a",
            left: `${pacmanSize + 20}px`,
            top: `${pacmanSize / 2 - 4}px`,
            animation: "ball1 0.5s infinite linear",
          }}
        />
        <div
          className="pac-man-ball2"
          style={{
            position: "absolute",
            borderRadius: "100em",
            width: "8px",
            height: "8px",
            backgroundColor: "#fed75a",
            left: `${pacmanSize + 40}px`,
            top: `${pacmanSize / 2 - 4}px`,
            animation: "ball2 0.5s 0.17s infinite linear",
          }}
        />
        <div
          className="pac-man-ball3"
          style={{
            position: "absolute",
            borderRadius: "100em",
            width: "8px",
            height: "8px",
            backgroundColor: "#fed75a",
            left: `${pacmanSize + 60}px`,
            top: `${pacmanSize / 2 - 4}px`,
            animation: "ball3 0.5s 0.33s infinite linear",
          }}
        />
      </div>
    </div>
  );
};
