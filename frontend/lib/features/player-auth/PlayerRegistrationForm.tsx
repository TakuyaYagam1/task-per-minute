"use client";

import Link from "next/link";
import { type FormEvent, useEffect, useRef, useState } from "react";

import { playerModel } from "../../entities/player";
import { isValidUsername } from "../../shared/lib";

import styles from "./PlayerAuthForm.module.css";

export function PlayerRegistrationForm() {
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [resendAvailable, setResendAvailable] = useState(false);
  const [accepted, setAccepted] = useState(false);
  const [loginHref, setLoginHref] = useState("/login");
  const requestController = useRef<AbortController | null>(null);

  useEffect(() => () => {
    requestController.current?.abort();
  }, []);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (pending) return;

    const cleanUsername = username.trim();
    const cleanEmail = email.trim();
    const passwordLength = Array.from(password).length;

    if (!isValidUsername(cleanUsername)) {
      setError("Логин должен содержать 2-50 латинских букв, цифр, _ или -.");
      return;
    }
    if (passwordLength < 15 || passwordLength > 128) {
      setError("Пароль должен содержать от 15 до 128 символов.");
      return;
    }
    if (password !== confirmPassword) {
      setError("Пароли не совпадают.");
      return;
    }

    setError(null);
    setResendAvailable(false);
    setPending(true);
    const controller = new AbortController();
    requestController.current = controller;
    const result = await playerModel.registerPlayer(
      cleanUsername,
      cleanEmail,
      password,
      controller.signal,
    );
    if (requestController.current === controller) {
      requestController.current = null;
    }
    if (controller.signal.aborted) return;
    setPending(false);

    if (result.kind === "accepted") {
      setAccepted(true);
      const nextPath = new URLSearchParams(window.location.search).get("next");
      setLoginHref(nextPath ? `/login?next=${encodeURIComponent(nextPath)}` : "/login");
      setUsername("");
      setEmail("");
      setPassword("");
      setConfirmPassword("");
    } else if (result.kind === "username_taken") {
      setError("Этот логин уже занят.");
    } else if (result.kind === "rate_limited") {
      setError("Слишком много попыток. Повторите позже.");
    } else if (result.kind === "unavailable") {
      setError("Регистрация временно недоступна. Попробуйте позже.");
      setResendAvailable(true);
    } else if (result.kind !== "aborted") {
      setError("Не удалось отправить запрос. Попробуйте позже.");
    }
  };

  if (accepted) {
    return (
      <div className={styles.form}>
        <p className={`${styles.message} ${styles.success}`} role="status" aria-live="polite">
          Если этот адрес можно зарегистрировать, мы отправили письмо со ссылкой для подтверждения.
        </p>
        <div className={styles.actions}>
          <Link className={styles.link} href={loginHref}>Перейти ко входу</Link>
          <Link href="/verify-email">Не пришло письмо?</Link>
        </div>
      </div>
    );
  }

  return (
    <form className={styles.form} onSubmit={submit} aria-busy={pending}>
      <div className={styles.field}>
        <label className={styles.label} htmlFor="register-username">Логин, видимый в рейтинге</label>
        <input
          className={styles.input}
          id="register-username"
          name="username"
          type="text"
          autoComplete="username"
          autoCapitalize="none"
          spellCheck={false}
          minLength={2}
          maxLength={50}
          pattern="[A-Za-z0-9_-]{2,50}"
          required
          value={username}
          onChange={(event) => setUsername(event.target.value)}
          disabled={pending}
        />
        <p className={styles.hint}>2-50 символов: латинские буквы, цифры, _ или -.</p>
      </div>

      <div className={styles.field}>
        <label className={styles.label} htmlFor="register-email">Email</label>
        <input
          className={styles.input}
          id="register-email"
          name="email"
          type="email"
          autoComplete="email"
          autoCapitalize="none"
          spellCheck={false}
          maxLength={254}
          required
          value={email}
          onChange={(event) => setEmail(event.target.value)}
          disabled={pending}
        />
      </div>

      <div className={styles.field}>
        <label className={styles.label} htmlFor="register-password">Пароль</label>
        <input
          className={styles.input}
          id="register-password"
          name="new-password"
          type="password"
          autoComplete="new-password"
          maxLength={256}
          required
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          disabled={pending}
        />
        <p className={styles.hint}>От 15 до 128 символов.</p>
      </div>

      <div className={styles.field}>
        <label className={styles.label} htmlFor="register-password-confirm">Повторите пароль</label>
        <input
          className={styles.input}
          id="register-password-confirm"
          name="confirm-password"
          type="password"
          autoComplete="new-password"
          maxLength={256}
          required
          value={confirmPassword}
          onChange={(event) => setConfirmPassword(event.target.value)}
          disabled={pending}
        />
      </div>

      {error ? <p className={`${styles.message} ${styles.error}`} role="alert">{error}</p> : null}
      {resendAvailable ? <Link href="/verify-email">Не пришло письмо?</Link> : null}

      <button className={`${styles.button} btn btn-primary`} type="submit" disabled={pending}>
        {pending ? "Отправка..." : "Создать аккаунт"}
      </button>
    </form>
  );
}
