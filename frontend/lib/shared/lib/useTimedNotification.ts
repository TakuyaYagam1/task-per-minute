"use client";

import { useCallback, useEffect, useRef, useState } from "react";

export const useTimedNotification = <TValue,>() => {
  const [notification, setNotificationState] = useState<TValue | null>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const generationRef = useRef(0);

  const clearTimer = useCallback(() => {
    if (timerRef.current) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  const scheduleClear = useCallback((generation: number, duration: number) => {
    if (duration <= 0) {
      return;
    }
    timerRef.current = setTimeout(() => {
      if (generationRef.current === generation) {
        timerRef.current = null;
        setNotificationState(null);
      }
    }, duration);
  }, []);

  const setNotification = useCallback(
    (value: TValue | null, duration = 3000) => {
      generationRef.current += 1;
      const generation = generationRef.current;
      clearTimer();
      setNotificationState(value);
      if (value !== null) {
        scheduleClear(generation, duration);
      }
    },
    [clearTimer, scheduleClear],
  );

  const showNotification = useCallback(
    (value: TValue, duration = 3000) => {
      generationRef.current += 1;
      const generation = generationRef.current;
      clearTimer();
      setNotificationState(value);
      scheduleClear(generation, duration);
    },
    [clearTimer, scheduleClear],
  );

  useEffect(() => {
    return () => {
      generationRef.current += 1;
      clearTimer();
    };
  }, [clearTimer]);

  return {
    notification,
    setNotification,
    showNotification,
  };
};
