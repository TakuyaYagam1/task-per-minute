"use client";

import { type FormEvent, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";

import { playerModel } from "../../entities/player";
import { getSafeArenaPublicReturnPath, getSafeArenaReturnPath } from "../../shared/lib";

import styles from "./PlayerAuthForm.module.css";

type ResendFeedback = "sent" | "rate_limited" | "already_verified" | "unavailable" | "error" | null;

const RESEND_COOLDOWN_SECONDS = 60;
const MAX_RESEND_COOLDOWN_SECONDS = 300;

const retryAfterSeconds = (
  retryAfter: string | null | undefined,
  fallback: number | null,
): number | null => {
  if (!retryAfter?.trim()) return fallback;

  const value = retryAfter.trim();
  if (/^\d+$/u.test(value)) {
    const seconds = Number(value);
    return Number.isSafeInteger(seconds) && seconds > 0
      ? Math.min(seconds, MAX_RESEND_COOLDOWN_SECONDS)
      : fallback;
  }

  const retryAt = Date.parse(value);
  if (!Number.isFinite(retryAt)) return fallback;
  const seconds = Math.ceil((retryAt - Date.now()) / 1000);
  if (seconds <= 0) return 0;
  return Math.min(seconds, MAX_RESEND_COOLDOWN_SECONDS);
};

export function PlayerLoginForm() {
  const router = useRouter();
  const [login, setLogin] = useState("");
  const [password, setPassword] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [activationNeeded, setActivationNeeded] = useState(false);
  const [resendPending, setResendPending] = useState(false);
  const [resendFeedback, setResendFeedback] = useState<ResendFeedback>(null);
  const [resendCooldownSeconds, setResendCooldownSeconds] = useState(0);
  const requestController = useRef<AbortController | null>(null);
  const resendController = useRef<AbortController | null>(null);
  const resendInFlight = useRef(false);
  const resendCooldownUntil = useRef(0);

  useEffect(() => () => {
    requestController.current?.abort();
    resendController.current?.abort();
  }, []);

  useEffect(() => {
    if (resendCooldownSeconds <= 0) return undefined;

    const timer = window.setTimeout(() => {
      const secondsLeft = Math.ceil((resendCooldownUntil.current - Date.now()) / 1000);
      resendCooldownUntil.current = secondsLeft > 0 ? resendCooldownUntil.current : 0;
      setResendCooldownSeconds(Math.max(0, secondsLeft));
    }, 1000);
    return () => window.clearTimeout(timer);
  }, [resendCooldownSeconds]);

  const startResendCooldown = (seconds: number | null) => {
    resendCooldownUntil.current = seconds && seconds > 0
      ? Date.now() + seconds * 1000
      : 0;
    setResendCooldownSeconds(seconds && seconds > 0 ? seconds : 0);
  };

  const resetActivationState = () => {
    const controller = resendController.current;
    resendController.current = null;
    resendInFlight.current = false;
    controller?.abort();
    resendCooldownUntil.current = 0;
    setActivationNeeded(false);
    setResendPending(false);
    setResendFeedback(null);
    setResendCooldownSeconds(0);
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (pending || resendInFlight.current) return;
    resetActivationState();
    if (!login.trim() || password.length === 0) {
      setError("Введите логин или email и пароль.");
      return;
    }

    setError(null);
    setPending(true);
    const controller = new AbortController();
    requestController.current = controller;
    const result = await playerModel.loginPlayer(
      login.trim(),
      password,
      controller.signal,
    );
    if (requestController.current === controller) {
      requestController.current = null;
    }
    if (controller.signal.aborted) return;
    setPending(false);

    if (result.kind === "ok") {
      const requestedPath = new URLSearchParams(window.location.search).get("next");
      const nextPath =
        getSafeArenaReturnPath(requestedPath, "participant") ??
        getSafeArenaPublicReturnPath(requestedPath);
      router.replace(nextPath ?? "/arena");
      return;
    }
    if (result.kind === "rate_limited") {
      setError("Слишком много попыток. Повторите позже.");
      return;
    }
    if (result.kind === "unavailable") {
      setError("Вход временно недоступен. Попробуйте позже.");
      return;
    }
    if (result.kind === "invalid_credentials") {
      setError("Неверный логин или пароль.");
      return;
    }
    if (result.kind === "email_unverified") {
      setError(null);
      setActivationNeeded(true);
      return;
    }
    if (result.kind !== "aborted") {
      setError("Не удалось войти. Попробуйте позже.");
    }
  };

  const resendVerification = async () => {
    if (
      !activationNeeded ||
      resendInFlight.current ||
      resendCooldownSeconds > 0
    ) {
      return;
    }

    resendInFlight.current = true;
    setResendPending(true);
    setResendFeedback(null);
    const controller = new AbortController();
    resendController.current = controller;
    const result = await playerModel.resendLoginVerification(
      login.trim(),
      password,
      controller.signal,
    );
    if (resendController.current === controller) {
      resendController.current = null;
    }
    if (controller.signal.aborted) return;

    resendInFlight.current = false;
    setResendPending(false);
    if (result.kind === "sent") {
      setResendFeedback("sent");
      startResendCooldown(RESEND_COOLDOWN_SECONDS);
    } else if (result.kind === "rate_limited") {
      setResendFeedback("rate_limited");
      startResendCooldown(
        retryAfterSeconds(result.retryAfter, RESEND_COOLDOWN_SECONDS) ?? RESEND_COOLDOWN_SECONDS,
      );
    } else if (result.kind === "already_verified") {
      setActivationNeeded(false);
      setResendFeedback("already_verified");
    } else if (result.kind === "invalid_credentials") {
      setActivationNeeded(false);
      setResendFeedback(null);
      setError("Неверный логин или пароль.");
    } else if (result.kind === "unavailable") {
      setResendFeedback("unavailable");
      startResendCooldown(retryAfterSeconds(result.retryAfter, null));
    } else if (result.kind !== "aborted") {
      setResendFeedback("error");
    }
  };

  const resetAfterCredentialEdit = () => {
    resetActivationState();
    setError(null);
  };

  return (
    <form
      className={styles.form}
      onSubmit={submit}
      aria-busy={pending || resendPending}
    >
      <div className={styles.field}>
        <label className={styles.label} htmlFor="login-name">Логин или email</label>
        <input
          className={styles.input}
          id="login-name"
          name="username"
          type="text"
          autoComplete="username"
          autoCapitalize="none"
          spellCheck={false}
          required
          value={login}
          onChange={(event) => {
            setLogin(event.target.value);
            resetAfterCredentialEdit();
          }}
          disabled={pending}
        />
      </div>

      <div className={styles.field}>
        <label className={styles.label} htmlFor="login-password">Пароль</label>
        <input
          className={styles.input}
          id="login-password"
          name="password"
          type="password"
          autoComplete="current-password"
          required
          value={password}
          onChange={(event) => {
            setPassword(event.target.value);
            resetAfterCredentialEdit();
          }}
          disabled={pending}
        />
      </div>

      {activationNeeded ? (
        <p className={`${styles.message} ${styles.error}`} role="alert">
          Аккаунт не активирован. Подтвердите email по ссылке из письма.
        </p>
      ) : null}
      {error ? <p className={`${styles.message} ${styles.error}`} role="alert">{error}</p> : null}
      {resendPending ? (
        <p className={styles.message} role="status" aria-live="polite">Отправляем письмо...</p>
      ) : null}
      {resendFeedback === "sent" ? (
        <p className={`${styles.message} ${styles.success}`} role="status" aria-live="polite">
          Письмо подтверждения отправлено. Проверьте почту.
        </p>
      ) : null}
      {resendFeedback === "already_verified" ? (
        <p className={`${styles.message} ${styles.success}`} role="status" aria-live="polite">
          Аккаунт уже подтвержден. Нажмите кнопку Войти.
        </p>
      ) : null}
      {resendFeedback === "rate_limited" && resendCooldownSeconds > 0 ? (
        <p className={`${styles.message} ${styles.error}`} role="alert">
          Слишком много попыток. Подождите перед повторным запросом.
        </p>
      ) : null}
      {resendFeedback === "unavailable" || resendFeedback === "error" ? (
        <p className={`${styles.message} ${styles.error}`} role="alert">
          Не удалось отправить письмо. Попробуйте позже.
        </p>
      ) : null}
      {resendCooldownSeconds > 0 ? (
        <p className={`${styles.hint} ${styles.cooldown}`} id="login-resend-cooldown" role="timer" aria-live="off">
          Повторная отправка доступна через {resendCooldownSeconds} с.
        </p>
      ) : null}
      {activationNeeded ? (
        <button
          className={`${styles.button} btn btn-secondary`}
          type="button"
          aria-describedby={resendCooldownSeconds > 0 ? "login-resend-cooldown" : undefined}
          disabled={pending || resendPending || resendCooldownSeconds > 0}
          onClick={resendVerification}
        >
          {resendPending ? "Отправка..." : "Отправить письмо подтверждения"}
        </button>
      ) : null}

      <button className={`${styles.button} btn btn-primary`} type="submit" disabled={pending || resendPending}>
        {pending ? "Вход..." : "Войти"}
      </button>
    </form>
  );
}
