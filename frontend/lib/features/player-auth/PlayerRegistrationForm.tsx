"use client";

import { type FormEvent, useEffect, useRef, useState } from "react";

import { playerModel } from "../../entities/player";
import { isValidUsername } from "../../shared/lib";

import styles from "./PlayerAuthForm.module.css";

type RegistrationField = "username" | "email" | "password" | "confirmPassword";
type RegistrationTouched = Record<RegistrationField, boolean>;

const EMAIL_LOCAL_PART_PATTERN = /^[A-Za-z0-9!#$%&'*+\/=?^_`{|}~.-]+$/u;
const DNS_LABEL_PATTERN = /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/u;
const PASSWORD_LOWERCASE_PATTERN = /\p{Ll}/u;
const PASSWORD_UPPERCASE_PATTERN = /\p{Lu}/u;
const PASSWORD_DECIMAL_PATTERN = /\p{Nd}/u;
const PASSWORD_PUNCTUATION_OR_SYMBOL_PATTERN = /[\p{P}\p{S}]/u;
const RESEND_COOLDOWN_SECONDS = 60;
const MAX_RESEND_COOLDOWN_SECONDS = 300;

const retryAfterSeconds = (value: string | null | undefined): number => {
  if (!value?.trim()) return RESEND_COOLDOWN_SECONDS;

  const normalized = value.trim();
  if (/^\d+$/u.test(normalized)) {
    const seconds = Number(normalized);
    return Number.isSafeInteger(seconds) && seconds > 0
      ? Math.min(seconds, MAX_RESEND_COOLDOWN_SECONDS)
      : RESEND_COOLDOWN_SECONDS;
  }

  const retryAt = Date.parse(normalized);
  if (!Number.isFinite(retryAt)) return RESEND_COOLDOWN_SECONDS;
  const seconds = Math.ceil((retryAt - Date.now()) / 1000);
  return seconds > 0 ? Math.min(seconds, MAX_RESEND_COOLDOWN_SECONDS) : RESEND_COOLDOWN_SECONDS;
};

const isValidUnicode = (value: string): boolean => {
  for (let index = 0; index < value.length; index += 1) {
    const codeUnit = value.charCodeAt(index);
    if (codeUnit >= 0xd800 && codeUnit <= 0xdbff) {
      const nextCodeUnit = value.charCodeAt(index + 1);
      if (!(nextCodeUnit >= 0xdc00 && nextCodeUnit <= 0xdfff)) return false;
      index += 1;
    } else if (codeUnit >= 0xdc00 && codeUnit <= 0xdfff) {
      return false;
    }
  }
  return true;
};

const registrationEmailError = (input: string): string | null => {
  const email = input.trim();
  if (!email) return "Введите email.";
  if (new TextEncoder().encode(email).length > 254) {
    return "Email слишком длинный.";
  }

  const separator = email.indexOf("@");
  if (separator <= 0 || separator !== email.lastIndexOf("@")) {
    return "Введите полный email, например name@example.com.";
  }

  const localPart = email.slice(0, separator);
  const domain = email.slice(separator + 1);
  if (
    localPart.length > 64 ||
    localPart.startsWith(".") ||
    localPart.endsWith(".") ||
    localPart.includes("..") ||
    !EMAIL_LOCAL_PART_PATTERN.test(localPart)
  ) {
    return "Введите полный email, например name@example.com.";
  }

  const labels = domain.split(".");
  if (
    labels.length < 2 ||
    labels.some((label) => label.length > 63 || !DNS_LABEL_PATTERN.test(label))
  ) {
    return "Введите полный email, например name@example.com.";
  }

  const finalLabel = labels[labels.length - 1];
  const isAsciiTopLevelDomain = /^[A-Za-z]{2,63}$/u.test(finalLabel);
  const isPunycodeTopLevelDomain = /^xn--.+$/iu.test(finalLabel);
  if (!isAsciiTopLevelDomain && !isPunycodeTopLevelDomain) {
    return "Введите полный email, например name@example.com.";
  }

  return null;
};

const registrationPasswordError = (password: string): string | null => {
  if (!password) return "Введите пароль.";
  if (!isValidUnicode(password)) return "Пароль содержит недопустимый символ.";
  if (password.includes("\u0000")) return "Пароль содержит недопустимый символ.";

  const length = Array.from(password).length;
  if (length < 10 || length > 128) {
    return "Пароль должен содержать от 10 до 128 символов.";
  }
  if (new TextEncoder().encode(password).length > 512) {
    return "Пароль слишком длинный.";
  }
  if (
    !PASSWORD_LOWERCASE_PATTERN.test(password) ||
    !PASSWORD_UPPERCASE_PATTERN.test(password) ||
    !PASSWORD_DECIMAL_PATTERN.test(password) ||
    !PASSWORD_PUNCTUATION_OR_SYMBOL_PATTERN.test(password)
  ) {
    return "Нужны строчная и заглавная буквы, цифра и спецсимвол.";
  }
  return null;
};

export function PlayerRegistrationForm() {
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [resendAvailable, setResendAvailable] = useState(false);
  const [resendEmail, setResendEmail] = useState<string | null>(null);
  const [resendPending, setResendPending] = useState(false);
  const [resendFeedback, setResendFeedback] = useState<"sent" | "rate_limited" | "error" | null>(null);
  const [resendCooldownSeconds, setResendCooldownSeconds] = useState(0);
  const [accepted, setAccepted] = useState(false);
  const [touched, setTouched] = useState<RegistrationTouched>({
    username: false,
    email: false,
    password: false,
    confirmPassword: false,
  });
  const [submitted, setSubmitted] = useState(false);
  const requestController = useRef<AbortController | null>(null);
  const resendController = useRef<AbortController | null>(null);
  const resendInFlight = useRef(false);
  const resendCooldownUntil = useRef(0);
  const usernameInput = useRef<HTMLInputElement>(null);
  const emailInput = useRef<HTMLInputElement>(null);
  const passwordInput = useRef<HTMLInputElement>(null);
  const confirmPasswordInput = useRef<HTMLInputElement>(null);

  const usernameError = touched.username || submitted
    ? !isValidUsername(username.trim())
      ? "Логин должен содержать 2-50 латинских букв, цифр, _ или -."
      : null
    : null;
  const emailError = touched.email || submitted ? registrationEmailError(email) : null;
  const passwordError = touched.password || submitted ? registrationPasswordError(password) : null;
  const confirmPasswordError = touched.confirmPassword || submitted
    ? !confirmPassword
      ? "Повторите пароль."
      : confirmPassword !== password
        ? "Пароли не совпадают."
        : null
    : null;

  useEffect(() => () => {
    requestController.current?.abort();
    resendController.current?.abort();
  }, []);

  const resendCooldownActive = resendCooldownSeconds > 0;
  useEffect(() => {
    if (!resendCooldownActive) return undefined;

    const timer = window.setInterval(() => {
      const secondsLeft = Math.ceil((resendCooldownUntil.current - Date.now()) / 1000);
      if (secondsLeft <= 0) {
        resendCooldownUntil.current = 0;
        setResendFeedback((current) => current === "rate_limited" ? null : current);
      }
      setResendCooldownSeconds(Math.max(0, secondsLeft));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [resendCooldownActive]);

  const startResendCooldown = (seconds: number): void => {
    resendCooldownUntil.current = Date.now() + seconds * 1000;
    setResendCooldownSeconds(seconds);
  };

  const resetResendState = (): void => {
    const controller = resendController.current;
    resendController.current = null;
    resendInFlight.current = false;
    controller?.abort();
    resendCooldownUntil.current = 0;
    setResendPending(false);
    setResendFeedback(null);
    setResendCooldownSeconds(0);
  };

  const resendVerification = async (): Promise<void> => {
    if (
      !resendAvailable ||
      !resendEmail ||
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
    const result = await playerModel.resendVerification(resendEmail, controller.signal);
    if (resendController.current === controller) {
      resendController.current = null;
    }
    if (controller.signal.aborted) return;

    resendInFlight.current = false;
    setResendPending(false);
    if (result.kind === "accepted") {
      setResendFeedback("sent");
      startResendCooldown(RESEND_COOLDOWN_SECONDS);
    } else if (result.kind === "rate_limited") {
      setResendFeedback("rate_limited");
      startResendCooldown(retryAfterSeconds(result.retryAfter));
    } else if (result.kind !== "aborted") {
      setResendFeedback("error");
    }
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (pending || resendInFlight.current) return;
    setSubmitted(true);

    const cleanUsername = username.trim();
    const cleanEmail = email.trim();
    const invalidUsername = !isValidUsername(cleanUsername);
    const invalidEmail = registrationEmailError(cleanEmail) !== null;
    const invalidPassword = registrationPasswordError(password) !== null;
    const invalidConfirmation = !confirmPassword || password !== confirmPassword;
    if (invalidUsername || invalidEmail || invalidPassword || invalidConfirmation) {
      if (invalidUsername) usernameInput.current?.focus();
      else if (invalidEmail) emailInput.current?.focus();
      else if (invalidPassword) passwordInput.current?.focus();
      else confirmPasswordInput.current?.focus();
      return;
    }

    setError(null);
    resetResendState();
    setResendAvailable(false);
    setResendEmail(null);
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
      setResendAvailable(true);
      setResendEmail(cleanEmail);
      startResendCooldown(RESEND_COOLDOWN_SECONDS);
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
      setResendEmail(cleanEmail);
      startResendCooldown(RESEND_COOLDOWN_SECONDS);
    } else if (result.kind !== "aborted") {
      setError("Не удалось отправить запрос. Попробуйте позже.");
    }
  };

  const resendControls = resendAvailable && resendEmail ? (
    <div className={styles.form}>
      {resendFeedback === "sent" ? (
        <p className={`${styles.message} ${styles.success}`} role="status" aria-live="polite">
          Письмо было повторно отправлено на указанную почту.
        </p>
      ) : null}
      {resendFeedback === "rate_limited" ? (
        <p className={`${styles.message} ${styles.error}`} role="alert">
          Слишком много попыток. Повторите позже.
        </p>
      ) : null}
      {resendFeedback === "error" ? (
        <p className={`${styles.message} ${styles.error}`} role="alert">
          Не удалось отправить письмо. Попробуйте позже.
        </p>
      ) : null}
      {resendCooldownSeconds > 0 ? (
        <p className={`${styles.hint} ${styles.cooldown}`} id="register-resend-cooldown" role="timer" aria-live="off">
          Повторная отправка доступна через {resendCooldownSeconds} с.
        </p>
      ) : null}
      <button
        className={`${styles.button} btn btn-secondary`}
        type="button"
        aria-describedby={resendCooldownSeconds > 0 ? "register-resend-cooldown" : undefined}
        disabled={pending || resendPending || resendCooldownSeconds > 0}
        onClick={resendVerification}
      >
        {resendPending ? "Отправка..." : "Не пришло письмо?"}
      </button>
    </div>
  ) : null;

  if (accepted) {
    return (
      <div className={styles.form}>
        {resendFeedback !== "sent" ? (
          <p className={`${styles.message} ${styles.success}`} role="status" aria-live="polite">
            Письмо было отправлено на указанную почту.
          </p>
        ) : null}
        {resendControls}
      </div>
    );
  }

  return (
    <form className={styles.form} onSubmit={submit} aria-busy={pending || resendPending} noValidate>
      <div className={styles.field}>
        <label className={styles.label} htmlFor="register-username">Логин, видимый в рейтинге</label>
        <input
          className={styles.input}
          id="register-username"
          ref={usernameInput}
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
          aria-invalid={usernameError ? true : undefined}
          aria-describedby={usernameError ? "register-username-hint register-username-error" : "register-username-hint"}
          onBlur={() => setTouched((current) => ({ ...current, username: true }))}
          onChange={(event) => {
            setUsername(event.target.value);
            setError(null);
          }}
          disabled={pending}
        />
        <p className={styles.hint} id="register-username-hint">2-50 символов: латинские буквы, цифры, _ или -.</p>
        {usernameError ? <p className={`${styles.hint} ${styles.errorText}`} id="register-username-error">{usernameError}</p> : null}
      </div>

      <div className={styles.field}>
        <label className={styles.label} htmlFor="register-email">Email</label>
        <input
          className={styles.input}
          id="register-email"
          ref={emailInput}
          name="email"
          type="email"
          autoComplete="email"
          autoCapitalize="none"
          spellCheck={false}
          required
          value={email}
          aria-invalid={emailError ? true : undefined}
          aria-describedby={emailError ? "register-email-hint register-email-error" : "register-email-hint"}
          onBlur={() => setTouched((current) => ({ ...current, email: true }))}
          onChange={(event) => {
            setEmail(event.target.value);
            setError(null);
            setResendAvailable(false);
            setResendEmail(null);
            resetResendState();
          }}
          disabled={pending}
        />
        <p className={styles.hint} id="register-email-hint">
          Например name@example.com.
        </p>
        {emailError ? <p className={`${styles.hint} ${styles.errorText}`} id="register-email-error">{emailError}</p> : null}
      </div>

      <div className={styles.field}>
        <label className={styles.label} htmlFor="register-password">Пароль</label>
        <input
          className={styles.input}
          id="register-password"
          ref={passwordInput}
          name="new-password"
          type="password"
          autoComplete="new-password"
          maxLength={256}
          required
          value={password}
          aria-invalid={passwordError ? true : undefined}
          aria-describedby={passwordError ? "register-password-hint register-password-error" : "register-password-hint"}
          onBlur={() => setTouched((current) => ({ ...current, password: true }))}
          onChange={(event) => {
            setPassword(event.target.value);
            setError(null);
          }}
          disabled={pending}
        />
        <p className={styles.hint} id="register-password-hint">
          10-128 символов, строчная и заглавная буквы, цифра и спецсимвол, например !, @ или #. Пробел не считается спецсимволом.
        </p>
        {passwordError ? <p className={`${styles.hint} ${styles.errorText}`} id="register-password-error">{passwordError}</p> : null}
      </div>

      <div className={styles.field}>
        <label className={styles.label} htmlFor="register-password-confirm">Повторите пароль</label>
        <input
          className={styles.input}
          id="register-password-confirm"
          ref={confirmPasswordInput}
          name="confirm-password"
          type="password"
          autoComplete="new-password"
          maxLength={256}
          required
          value={confirmPassword}
          aria-invalid={confirmPasswordError ? true : undefined}
          aria-describedby={confirmPasswordError ? "register-password-confirm-error" : undefined}
          onBlur={() => setTouched((current) => ({ ...current, confirmPassword: true }))}
          onChange={(event) => {
            setConfirmPassword(event.target.value);
            setError(null);
          }}
          disabled={pending}
        />
        {confirmPasswordError ? <p className={`${styles.hint} ${styles.errorText}`} id="register-password-confirm-error">{confirmPasswordError}</p> : null}
      </div>

      {error ? <p className={`${styles.message} ${styles.error}`} role="alert">{error}</p> : null}
      {resendControls}

      <button className={`${styles.button} btn btn-primary`} type="submit" disabled={pending || resendPending}>
        {pending ? "Отправка..." : "Создать аккаунт"}
      </button>
    </form>
  );
}
