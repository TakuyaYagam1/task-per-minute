"use client";

import { type FormEvent, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";

import { playerModel } from "../../entities/player";
import { getSafeArenaPublicReturnPath, getSafeArenaReturnPath } from "../../shared/lib";

import styles from "./PlayerAuthForm.module.css";

export function PlayerLoginForm() {
  const router = useRouter();
  const [login, setLogin] = useState("");
  const [password, setPassword] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const requestController = useRef<AbortController | null>(null);

  useEffect(() => () => {
    requestController.current?.abort();
  }, []);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (pending) return;
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
      router.replace(nextPath ?? "/");
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
      setError("Проверьте логин, пароль и подтверждение email.");
      return;
    }
    if (result.kind !== "aborted") {
      setError("Не удалось войти. Попробуйте позже.");
    }
  };

  return (
    <>
      <form className={styles.form} onSubmit={submit} aria-busy={pending}>
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
            onChange={(event) => setLogin(event.target.value)}
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
            onChange={(event) => setPassword(event.target.value)}
            disabled={pending}
          />
        </div>

        {error ? <p className={`${styles.message} ${styles.error}`} role="alert">{error}</p> : null}

        <button className={`${styles.button} btn btn-primary`} type="submit" disabled={pending}>
          {pending ? "Вход..." : "Войти"}
        </button>
      </form>
    </>
  );
}
