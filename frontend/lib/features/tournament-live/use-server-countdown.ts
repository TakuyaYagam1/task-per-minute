"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  createServerCountdown,
  readMonotonicNow,
  remainingMsAt,
  viewServerCountdown,
  type CountdownView,
  type MonotonicClock,
  type ServerCountdown,
} from "./countdown";

export type UseServerCountdownOptions = Readonly<{
  serverTimestamp: string | number | Date;
  deadline: string | number | Date;
  receivedAtMonotonicMs?: number;
  monotonicNow?: MonotonicClock;
  intervalMs?: number;
}>;

const safeIntervalMs = (value: number | undefined): number => {
  if (value === undefined) {
    return 250;
  }
  if (!Number.isFinite(value) || value < 50) {
    throw new TypeError("intervalMs must be at least 50ms");
  }
  return value;
};

export const keepCurrentCountdownUnlessCandidateIsShorter = (
  current: ServerCountdown,
  candidate: ServerCountdown,
  monotonicTime: number,
): ServerCountdown => {
  if (candidate.serverTimestampMs < current.serverTimestampMs) {
    return current;
  }
  const currentRemainingMs = remainingMsAt(current, monotonicTime);
  const candidateRemainingMs = remainingMsAt(candidate, monotonicTime);
  if (
    candidate.serverTimestampMs === current.serverTimestampMs &&
    candidateRemainingMs > currentRemainingMs
  ) {
    return current;
  }
  if (candidate.deadlineMs !== current.deadlineMs) {
    return candidate;
  }
  return candidateRemainingMs <= currentRemainingMs ? candidate : current;
};

export const useServerCountdown = (
  options: UseServerCountdownOptions,
): CountdownView => {
  const { deadline, receivedAtMonotonicMs, serverTimestamp } = options;
  const monotonicNowRef = useRef(options.monotonicNow ?? readMonotonicNow);
  monotonicNowRef.current = options.monotonicNow ?? readMonotonicNow;
  const monotonicNow = useCallback(() => monotonicNowRef.current(), []);
  const intervalMs = safeIntervalMs(options.intervalMs);
  const countdown = useMemo<ServerCountdown>(
    () => createServerCountdown({ deadline, receivedAtMonotonicMs, serverTimestamp }, monotonicNow),
    [deadline, receivedAtMonotonicMs, serverTimestamp, monotonicNow],
  );
  const [effectiveCountdown, setEffectiveCountdown] = useState<ServerCountdown>(countdown);
  const [view, setView] = useState<CountdownView>(() =>
    viewServerCountdown(effectiveCountdown, monotonicNow()),
  );

  useEffect(() => {
    const monotonicTime = monotonicNow();
    setEffectiveCountdown((current) => keepCurrentCountdownUnlessCandidateIsShorter(
      current,
      countdown,
      monotonicTime,
    ));
  }, [countdown, monotonicNow]);

  useEffect(() => {
    let mounted = true;
    const update = (): void => {
      if (mounted) {
        setView(viewServerCountdown(effectiveCountdown, monotonicNow()));
      }
    };
    const onVisibilityChange = (): void => {
      if (document.visibilityState === "visible") {
        update();
      }
    };

    update();
    const timer = window.setInterval(update, intervalMs);
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      mounted = false;
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [effectiveCountdown, intervalMs, monotonicNow]);

  return view;
};
