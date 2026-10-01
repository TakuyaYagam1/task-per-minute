"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { type FormEvent, useEffect, useLayoutEffect, useRef, useState } from "react";

import { playerModel } from "../../entities/player";

import styles from "./PlayerAuthForm.module.css";

type VerificationState = "reading" | "missing" | "ready" | "pending" | "confirmed" | "invalid";

export function EmailVerificationPanel() {
  const router = useRouter();
  const [token, setToken] = useState<string | null>(null);
  const [verificationState, setVerificationState] = useState<VerificationState>("reading");
  const [verificationError, setVerificationError] = useState<string | null>(null);
  const fragmentRead = useRef(false);
  const confirmationStarted = useRef(false);
  const verificationController = useRef<AbortController | null>(null);

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
  }, []);

  useEffect(() => {
    if (verificationState !== "confirmed") return undefined;

    const redirectTimer = window.setTimeout(() => router.replace("/login"), 5000);
    return () => window.clearTimeout(redirectTimer);
  }, [router, verificationState]);

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

  return (
    <div className={styles.form}>
      {verificationState === "reading" ? (
        <p className={styles.message} role="status">Подготавливаем подтверждение...</p>
      ) : null}

      {verificationState === "ready" || verificationState === "pending" ? (
        <form onSubmit={confirm} aria-busy={verificationState === "pending"}>
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
        <>
          <p className={`${styles.message} ${styles.success}`} role="status" aria-live="polite">
            Email подтверждён. Переход ко входу через 5 секунд.
          </p>
          <Link className={`${styles.button} btn btn-primary`} href="/login">
            Войти
          </Link>
        </>
      ) : null}

      {verificationState === "missing" ? (
        <p className={styles.message} role="status">
          В ссылке нет кода подтверждения.
        </p>
      ) : null}

      {verificationError ? (
        <p className={`${styles.message} ${styles.error}`} role="alert">{verificationError}</p>
      ) : null}
    </div>
  );
}
