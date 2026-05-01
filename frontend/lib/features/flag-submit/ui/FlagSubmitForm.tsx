"use client";

import React, { useState } from "react";
import { FlagStatus } from "../../../shared/types";
import { Button } from "../../../shared/ui";

interface FlagSubmitFormProps {
  onSubmit: (flag: string) => void;
  isSubmitting: boolean;
  flagStatus: FlagStatus;
  disabled?: boolean;
}

const STYLES = {
  container: {
    backgroundColor: "rgba(112, 206, 206, 0.8)",
    borderRadius: "1rem",
    padding: "1.5rem",
    boxShadow: "0 8px 32px rgba(0, 0, 0, 0.6)",
    color: "#FFF",
  },

  formGroup: {
    display: "flex",
    gap: "1rem",
    alignItems: "end",
    flexWrap: "wrap" as const,
  },

  inputGroup: {
    flex: 1,
    minWidth: "250px",
  },

  input: {
    width: "100%",
    padding: "0.75rem 1rem",
    borderRadius: "0.5rem",
    border: "1px solid rgba(255, 255, 255, 0.3)",
    backgroundColor: "rgba(255, 255, 255, 0.1)",
    color: "#FFF",
    fontSize: "1rem",
    fontFamily: "inherit",
    boxSizing: "border-box" as const,
  },

  statusMessage: {
    display: "flex",
    alignItems: "center",
    marginTop: "0.75rem",
    padding: "0.5rem",
    borderRadius: "0.5rem",
    border: "1px solid",
  },
};

// Форма для отправки флага с валидацией и отображением статуса
export const FlagSubmitForm: React.FC<FlagSubmitFormProps> = ({
  onSubmit,
  isSubmitting,
  flagStatus,
  disabled = false,
}) => {
  const [flag, setFlag] = useState("");

  // Обрабатывает отправку флага
  const handleSubmit = () => {
    if (flag.trim()) {
      onSubmit(flag.trim());
    }
  };

  // Обрабатывает нажатие клавиш (Enter для отправки)
  const handleKeyPress = (e: React.KeyboardEvent) => {
    if (e.key === "Enter") {
      handleSubmit();
    }
  };

  // Возвращает стили для статуса флага
  const getStatusStyle = () => {
    if (flagStatus === "correct") {
      return {
        backgroundColor: "rgba(76, 175, 80, 0.1)",
        borderColor: "#4CAF50",
        color: "#4CAF50",
      };
    }
    if (flagStatus === "incorrect") {
      return {
        backgroundColor: "rgba(244, 67, 54, 0.1)",
        borderColor: "#F44336",
        color: "#F44336",
      };
    }
    return {};
  };

  return (
    <div style={STYLES.container}>
      <div style={{ padding: "0.75rem" }}>
        <h2
          style={{
            fontSize: "1.5rem",
            fontWeight: 600,
            marginBottom: "0.5rem",
            color: "#72D1EB",
          }}
        >
          Отправка флага
        </h2>

        <div style={STYLES.formGroup}>
          <div style={STYLES.inputGroup}>
            <label
              style={{
                display: "block",
                marginBottom: "0.5rem",
                fontSize: "1rem",
                fontWeight: 500,
              }}
            >
              Введите найденный флаг:
            </label>
            <input
              type="text"
              value={flag}
              onChange={(e) => setFlag(e.target.value)}
              onKeyPress={handleKeyPress}
              style={STYLES.input}
              disabled={disabled}
              placeholder="CTF{...}"
            />
          </div>

          <Button
            onClick={handleSubmit}
            disabled={isSubmitting || disabled || !flag.trim()}
            variant="success"
          >
            {isSubmitting ? "ОТПРАВКА..." : "ОТПРАВИТЬ ФЛАГ"}
          </Button>
        </div>

        <p
          style={{
            marginTop: "1rem",
            fontSize: "0.9rem",
            color: "rgba(255, 255, 255, 0.7)",
            fontStyle: "italic",
            margin: "1rem 0 0 0",
          }}
        >
          Флаг должен быть в формате flag&#123;...&#125;
        </p>

        {flagStatus !== "idle" && (
          <div style={{ ...STYLES.statusMessage, ...getStatusStyle() }}>
            <span
              style={{
                fontSize: "1.2rem",
                marginRight: "0.5rem",
              }}
            >
              {flagStatus === "correct" ? "✓" : "✗"}
            </span>
            <span style={{ fontWeight: 600 }}>
              {flagStatus === "correct" ? "Флаг верный!" : "Неверный флаг"}
            </span>
          </div>
        )}
      </div>
    </div>
  );
};
