"use client";

import React, { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

import { Button } from "../../../shared/ui";
import styles from "./error-page.module.css";

type StatusCopy = {
  title: string;
  description: string;
};

const STATUS_COPY: Record<number, StatusCopy> = {
  404: {
    title: "Страница не найдена",
    description: "Похоже, такой задачи в нашем CTF нет.",
  },
  409: {
    title: "Конфликт",
    description: "Кажется, действие уже выполнено или вы уже в дуэли.",
  },
  429: {
    title: "Слишком много запросов",
    description: "Подождите немного и попробуйте снова.",
  },
  500: {
    title: "Внутренняя ошибка",
    description: "Что-то сломалось у нас на сервере. Попробуйте ещё раз через минуту.",
  },
};

const DEFAULT_COPY: StatusCopy = {
  title: "Что-то пошло не так",
  description: "Попробуйте обновить страницу или вернуться назад.",
};

const formatRetry = (seconds: number): string => {
  if (seconds <= 0) {
    return "сейчас";
  }
  if (seconds < 60) {
    return `через ${seconds} с`;
  }
  const minutes = Math.ceil(seconds / 60);
  return `через ${minutes} мин`;
};

export interface ErrorPageShellProps {
  statusCode: number;
  title?: string;
  description?: string;
  retryAfterSeconds?: number;
  onRetry?: () => void;
}

export const ErrorPageShell: React.FC<ErrorPageShellProps> = ({
  statusCode,
  title,
  description,
  retryAfterSeconds,
  onRetry,
}) => {
  const router = useRouter();
  const copy = STATUS_COPY[statusCode] ?? DEFAULT_COPY;
  const initialRetry = retryAfterSeconds && retryAfterSeconds > 0 ? retryAfterSeconds : null;
  const [retryRemaining, setRetryRemaining] = useState<number | null>(initialRetry);

  useEffect(() => {
    setRetryRemaining(initialRetry);
  }, [initialRetry]);

  useEffect(() => {
    if (initialRetry === null) {
      return;
    }
    const timer = setInterval(() => {
      setRetryRemaining((prev) => {
        if (prev === null) {
          return null;
        }
        if (prev <= 1) {
          clearInterval(timer);
          return 0;
        }
        return prev - 1;
      });
    }, 1000);
    return () => clearInterval(timer);
  }, [initialRetry]);

  const handleBack = () => {
    if (typeof window !== "undefined" && window.history.length > 1) {
      router.back();
    } else {
      router.push("/");
    }
  };

  const handleHome = () => {
    router.push("/");
  };

  return (
    <main className={styles.root}>
      <section className={styles.shell} role="alert" aria-live="polite">
        <p className={styles.code} aria-label={`Ошибка ${statusCode}`}>
          {statusCode}
        </p>
        <h1 className={styles.title}>{title ?? copy.title}</h1>
        <p className={styles.description}>{description ?? copy.description}</p>
        {retryRemaining !== null && (
          <p className={styles.retry}>
            Можно повторить {formatRetry(retryRemaining)}
          </p>
        )}
        <div className={styles.actions}>
          {onRetry && (
            <Button onClick={onRetry} variant="primary">
              Попробовать снова
            </Button>
          )}
          <Button onClick={handleBack} variant="primary">
            Вернуться назад
          </Button>
          <Button onClick={handleHome} variant="secondary">
            На главную
          </Button>
        </div>
      </section>
    </main>
  );
};
