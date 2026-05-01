"use client";

import React, { useState } from "react";
import { Button } from "../../../shared/ui";

interface TaskDescriptionProps {
  title?: string;
  description?: string;
  taskUrl?: string;
}

const STYLES = {
  card: {
    backgroundColor: "rgba(112, 206, 206, 0.8)",
    borderRadius: "1rem",
    padding: "1.5rem",
    boxShadow: "0 8px 32px rgba(0, 0, 0, 0.6)",
    color: "#FFF",
  },
};

// Компонент для отображения описания задания и кнопки перехода к заданию
export const TaskDescription: React.FC<TaskDescriptionProps> = ({
  title = "Web Challenge",
  description = "Найдите скрытый флаг на веб-странице. Изучите исходный код, проверьте различные элементы страницы и найдите флаг в формате flag{...}",
  taskUrl,
}) => {
  const [taskOpened, setTaskOpened] = useState(false);

  // Открывает задание в новой вкладке
  const handleOpenTask = () => {
    setTaskOpened(true);
    if (taskUrl) {
      window.open(taskUrl, "_blank");
    } else {
      window.open("http://challenge.example.com", "_blank");
    }
  };

  return (
    <div style={STYLES.card}>
      <div style={{ padding: "0.75rem" }}>
        <h2
          style={{
            fontSize: "1.5rem",
            fontWeight: 600,
            marginBottom: "0.5rem",
            color: "#72D1EB",
          }}
        >
          Описание задания
        </h2>

        <div
          style={{
            fontSize: "1.1rem",
            lineHeight: "1.6",
            marginBottom: "0.75rem",
          }}
        >
          <p style={{ margin: "0 0 1rem 0" }}>{description}</p>
        </div>

        <Button
          onClick={handleOpenTask}
          variant={taskOpened ? "success" : "primary"}
          style={{
            width: "100%",
            boxShadow: taskOpened
              ? "0 4px 15px rgba(76, 175, 80, 0.3)"
              : "0 4px 15px rgba(114, 209, 235, 0.3)",
          }}
        >
          {taskOpened ? "✓ ЗАДАНИЕ ОТКРЫТО" : "ПЕРЕЙТИ К ЗАДАНИЮ"}
        </Button>
      </div>
    </div>
  );
};
