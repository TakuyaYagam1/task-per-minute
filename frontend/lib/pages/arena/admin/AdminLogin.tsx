"use client";

import type { FormEvent, RefObject } from "react";

import { Button } from "../../../shared/ui";

import styles from "../admin.module.css";

export interface AdminLoginProps {
  password: string;
  error: string | null;
  loading: boolean;
  logoutPending: boolean;
  passwordInputRef: RefObject<HTMLInputElement | null>;
  onPasswordChange: (password: string) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
}

export function AdminLogin({
  password,
  error,
  loading,
  logoutPending,
  passwordInputRef,
  onPasswordChange,
  onSubmit,
}: AdminLoginProps) {
  return (
    <section className={styles.loginPanel} aria-labelledby="admin-login-title">
      <h1 id="admin-login-title" className={styles.loginTitle}>
        Вход администратора
      </h1>
      <form onSubmit={onSubmit} className={styles.form} noValidate>
        <div className={styles.inputGroup}>
          <label htmlFor="admin-password">Пароль администратора</label>
          <input
            ref={passwordInputRef}
            id="admin-password"
            name="password"
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(event) => onPasswordChange(event.target.value)}
            placeholder="Введите пароль..."
            className={error ? styles.inputError : undefined}
            aria-invalid={Boolean(error)}
            aria-describedby={error ? "admin-password-error" : undefined}
          />
          {error && (
            <p id="admin-password-error" className={styles.fieldError} role="alert">
              {error}
            </p>
          )}
        </div>
        <Button
          type="submit"
          variant="primary"
          size="large"
          loading={loading || logoutPending}
          loadingLabel={logoutPending ? "Выход..." : "Вход..."}
          disabled={!password.trim()}
        >
          Войти
        </Button>
      </form>
    </section>
  );
}
