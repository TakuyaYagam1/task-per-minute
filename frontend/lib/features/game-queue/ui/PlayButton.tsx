"use client";

import React from "react";

interface PlayButtonProps {
  onClick: () => void;
  disabled?: boolean;
}

// Кнопка для начала поиска игры с анимациями и состояниями
export const PlayButton: React.FC<PlayButtonProps> = ({
  onClick,
  disabled = false,
}) => {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className={`
        btn btn-primary w-full text-lg font-bold py-4 px-8 
        transition-all duration-300 ease-out transform
        ${
          disabled
            ? "opacity-50 cursor-not-allowed"
            : "hover:scale-105 hover:shadow-lg animate-glow"
        }
        ${disabled ? "" : "active:scale-95"}
      `}
    >
      <span className={`${disabled ? "" : "animate-pulse"}`}>
        {disabled ? (
          <span className="flex items-center justify-center gap-2">
            <div className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin"></div>
            ЗАГРУЗКА...
          </span>
        ) : (
          <span className="flex items-center justify-center gap-2">
            🚀 ИГРАТЬ
          </span>
        )}
      </span>
    </button>
  );
};
