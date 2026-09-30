"use client";

import { type FormEvent, useEffect, useLayoutEffect, useRef, useState } from "react";

import { playerModel } from "../../entities/player";

import styles from "./PlayerAuthForm.module.css";

type VerificationState = "reading" | "missing" | "ready" | "pending" | "confirmed" | "invalid";

export function EmailVerificationPanel() {
  const [token, setToken] = useState<string | null>(null);
  const [verificationState, setVerificationState] = useState<VerificationState>("reading");
  const [verificationError, setVerificationError] = useState<string | null>(null);
  const [email, setEmail] = useState("");
  const [resendPending, setResendPending] = useState(false);
  const [resendState, setResendState] = useState<"idle" | "accepted" | "error">("idle");
  const fragmentRead = useRef(false);
  const confirmationStarted = useRef(false);
  const verificationController = useRef<AbortController | null>(null);
  const resendController = useRef<AbortController | null>(null);

  useLayoutEffect(() => {
    if (fragmentRead.current) return;
    fragmentRead.current = true;

    const fragment = window.location.hash;
    const parameters = new URLSearchParams(fragment.startsWith("#") ? fragment.slice(1) : fragment);
    const tokens = parameters.getAll("token");
    const extractedToken = tokens.length === 1 ? tokens[0] : null;

    if (fragment) {
      window.history.replaceState(
        window.history.state,
        "",
        `${window.location.pathname}${window.location.search}`,
      );
    }

    if (extractedToken?.trim()) {
      setToken(extractedToken);
      setVerificationState("ready");
    } else {
      setVerificationState("missing");
    }
  }, []);

  useEffect(() => () => {
    verificationController.current?.abort();
    resendController.current?.abort();
  }, []);

  const confirm = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!token || confirmationStarted.current) return;
    confirmationStarted.current = true;
    setVerificationError(null);
    setVerificationState("pending");

    const controller = new AbortController();
    verificationController.current = controller;
    const result = await playerModel.verifyEmail(token, controller.signal);
    if (verificationController.current === controller) {
      verificationController.current = null;
    }
    if (controller.signal.aborted) return;

    if (result.kind === "ok") {
      setToken(null);
      setVerificationState("confirmed");
    } else if (result.kind === "invalid") {
      setToken(null);
      setVerificationState("invalid");
      setVerificationError("Ссылка недействительна, уже использована или больше не действует.");
    } else if (result.kind === "rate_limited") {
      confirmationStarted.current = false;
      setVerificationState("ready");
      setVerificationError("Слишком много попыток. Повторите позже.");
    } else if (result.kind !== "aborted") {
      confirmationStarted.current = false;
      setVerificationState("ready");
      setVerificationError("Не удалось подтвердить email. Попробуйте позже.");
    }
  };

  const resend = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (resendPending || !email.trim()) return;
    setResendState("idle");
    setResendPending(true);

    const controller = new AbortController();
    resendController.current = controller;
    const result = await playerModel.resendVerification(email.trim(), controller.signal);
    if (resendController.current === controller) {
      resendController.current = null;
    }
    if (controller.signal.aborted) return;
    setResendPending(false);

    if (result.kind === "accepted") {
      setResendState("accepted");
    } else if (result.kind !== "aborted") {
      setResendState("error");
    }
  };

  return (
    <div className={styles.form}>
      {verificationState === "reading" ? (
        <p className={styles.message} role="status">Подготавливаем подтверждение...</p>
      ) : null}

      {verificationState === "ready" || verificationState === "pending" ? (
        <form onSubmit={confirm} aria-busy={verificationState === "pending"}>
          <p className={styles.message}>
            Подтвердите адрес email, чтобы завершить регистрацию.
          </p>
          <button
            className={`${styles.button} btn btn-primary`}
            type="submit"
            disabled={verificationState === "pending"}
          >
            {verificationState === "pending"
              ? "Подтверждение..."
              : verificationError
                ? "Повторить подтверждение"
                : "Подтвердить email"}
          </button>
        </form>
      ) : null}

      {verificationState === "confirmed" ? (
        <p className={`${styles.message} ${styles.success}`} role="status" aria-live="polite">
          Email подтверждён. Теперь можно войти.
        </p>
      ) : null}

      {verificationState === "missing" ? (
        <p className={styles.message} role="status">
          В ссылке нет кода подтверждения. Можно запросить новое письмо ниже.
        </p>
      ) : null}

      {verificationError ? (
        <p className={`${styles.message} ${styles.error}`} role="alert">{verificationError}</p>
      ) : null}

      {verificationState !== "confirmed" ? (
        <form className={styles.form} onSubmit={resend} aria-busy={resendPending}>
          <div className={styles.field}>
            <label className={styles.label} htmlFor="verification-email">Email для нового письма</label>
            <input
              className={styles.input}
              id="verification-email"
              name="email"
              type="email"
              autoComplete="email"
              autoCapitalize="none"
              spellCheck={false}
              maxLength={254}
              required
              value={email}
              onChange={(event) => {
                setEmail(event.target.value);
                setResendState("idle");
              }}
              disabled={resendPending}
            />
          </div>
          {resendState === "accepted" ? (
            <p className={`${styles.message} ${styles.success}`} role="status" aria-live="polite">
              Если для этого адреса доступно подтверждение, мы отправили письмо.
            </p>
          ) : null}
          {resendState === "error" ? (
            <p className={`${styles.message} ${styles.error}`} role="alert">
              Не удалось отправить письмо. Попробуйте позже.
            </p>
          ) : null}
          <button className={`${styles.button} btn btn-secondary`} type="submit" disabled={resendPending}>
            {resendPending ? "Отправка..." : "Отправить новое письмо"}
          </button>
        </form>
      ) : null}
    </div>
  );
}
