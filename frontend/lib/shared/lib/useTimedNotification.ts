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

  const setNotification = useCallback(
    (value: TValue | null) => {
      generationRef.current += 1;
      clearTimer();
      setNotificationState(value);
    },
    [clearTimer],
  );

  const showNotification = useCallback(
    (value: TValue, duration = 3000) => {
      generationRef.current += 1;
      const generation = generationRef.current;
      clearTimer();
      setNotificationState(value);
      timerRef.current = setTimeout(() => {
        if (generationRef.current === generation) {
          timerRef.current = null;
          setNotificationState(null);
        }
      }, duration);
    },
    [clearTimer],
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
