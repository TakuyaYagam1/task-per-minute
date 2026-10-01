"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import {
  type FocusEvent as ReactFocusEvent,
  type FormEvent,
  type Ref,
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

import { playerModel, type AccountMutationResult } from "../../entities/player";
import { usePlayerNotificationsCenter, useSiteHeaderAuth } from "../../features/site-header";
import { isValidUsername } from "../../shared/lib";
import type { PlayerNotification } from "../../shared/api";
import type { PlayerAccountSettingsResponse } from "../../shared/api/player";
import type { Player } from "../../shared/types";
import { ThemeToggle } from "../../shared/ui";

import { AvatarEditor } from "./AvatarEditor";
import styles from "./SettingsPage.module.css";
import { settingsEmailCodeError, settingsEmailError, settingsPasswordError } from "./validation";

type SettingsSection = "profile" | "security" | "appearance" | "notifications";
type RestoreState = "loading" | "ready" | "error";
type EditMode = "username" | "email" | "password" | null;
type EmailStep = "address" | "code";
type TouchedFields = {
  username: boolean;
  usernameCurrentPassword: boolean;
  email: boolean;
  emailCurrentPassword: boolean;
  emailCode: boolean;
  currentPassword: boolean;
  newPassword: boolean;
  repeatPassword: boolean;
};

const THEME_STORAGE_KEY = "task-per-minute-theme";
const EMPTY_TOUCHED_FIELDS: TouchedFields = {
  username: false,
  usernameCurrentPassword: false,
  email: false,
  emailCurrentPassword: false,
  emailCode: false,
  currentPassword: false,
  newPassword: false,
  repeatPassword: false,
};
const EMPTY_PASSWORDS = { current: "", next: "", repeat: "" };
const USERNAME_ERROR = "Логин должен содержать 2-50 латинских букв, цифр, _ или -.";
const PASSWORD_HINT = "10-128 символов, строчная и заглавная буквы, цифра и спецсимвол.";
const EMAIL_CODE_HINT = "Введите код из письма.";
const CURRENT_PASSWORD_ERROR = "Текущий пароль указан неверно.";
const USERNAME_TAKEN_ERROR = "Этот логин уже занят.";
const EMAIL_TAKEN_ERROR = "Этот адрес уже используется.";
const ACCOUNT_UNAVAILABLE_ERROR = "Не удалось сохранить изменения. Попробуйте позже.";
const notificationDate = (value: string): string =>
  new Date(value).toLocaleString("ru-RU", {
    day: "2-digit",
    month: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });

const sectionLabels: ReadonlyArray<{ id: SettingsSection; label: string }> = [
  { id: "profile", label: "Профиль" },
  { id: "security", label: "Безопасность" },
  { id: "appearance", label: "Оформление" },
  { id: "notifications", label: "Уведомления" },
];

const settingsSectionFromSearch = (value: string | null): SettingsSection =>
  value === "security" || value === "appearance" || value === "notifications"
    ? value
    : "profile";

const parseRetryAfterSeconds = (value?: string | null): number | null => {
  if (!value) return null;
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds > 0) return Math.ceil(seconds);
  const date = Date.parse(value);
  if (!Number.isFinite(date)) return null;
  return Math.max(1, Math.ceil((date - Date.now()) / 1000));
};

function PasswordField({
  id,
  label,
  value,
  autoComplete,
  hint,
  error,
  inputRef,
  onChange,
  onBlur,
}: Readonly<{
  id: string;
  label: string;
  value: string;
  autoComplete: "current-password" | "new-password";
  hint?: string;
  error?: string | null;
  inputRef?: Ref<HTMLInputElement>;
  onChange: (value: string) => void;
  onBlur: (event: ReactFocusEvent<HTMLInputElement>) => void;
}>) {
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  const describedBy = [hint ? hintId : null, error ? errorId : null]
    .filter(Boolean)
    .join(" ") || undefined;

  return (
    <div className={styles.field}>
      <label className={styles.label} htmlFor={id}>{label}</label>
      <input
        id={id}
        ref={inputRef}
        className={styles.input}
        type="password"
        autoComplete={autoComplete}
        autoCapitalize="none"
        spellCheck={false}
        value={value}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy}
        onChange={(event) => onChange(event.target.value)}
        onBlur={onBlur}
      />
      {hint ? <p className={styles.hint} id={hintId}>{hint}</p> : null}
      {error ? <p className={styles.errorText} id={errorId}>{error}</p> : null}
    </div>
  );
}

function AccountAccessNotice({
  player,
  restoreState,
  onRetry,
}: Readonly<{
  player: Player | null;
  restoreState: RestoreState;
  onRetry: () => void;
}>) {
  if (restoreState === "loading") {
    return <p className={styles.statusMessage} role="status">Проверяем сессию...</p>;
  }

  if (restoreState === "error") {
    return (
      <div className={styles.notice} role="alert">
        <p>Не удалось проверить сессию. Попробуйте еще раз.</p>
        <button type="button" className={styles.textButton} onClick={onRetry}>Повторить</button>
      </div>
    );
  }

  if (!player) {
    return (
      <div className={styles.guestNotice}>
        <p>Войдите, чтобы просмотреть данные аккаунта.</p>
        <Link href="/login" className={styles.loginLink}>Войти</Link>
      </div>
    );
  }

  return null;
}

export function SettingsPage() {
  const idPrefix = useId();
  const router = useRouter();
  const searchParams = useSearchParams();
  const searchParamsString = searchParams.toString();
  const notifications = usePlayerNotificationsCenter();
  const setNotificationsEnabled = notifications.setEnabled;
  const notificationStatus = notifications.status;
  const activeSection = settingsSectionFromSearch(searchParams.get("section"));
  const previousSectionRef = useRef(activeSection);
  const [player, setPlayer] = useState<Player | null>(null);
  const [accountSettings, setAccountSettings] = useState<PlayerAccountSettingsResponse | null>(null);
  const [restoreState, setRestoreState] = useState<RestoreState>("loading");
  const [restoreAttempt, setRestoreAttempt] = useState(0);
  const [logoutPending, setLogoutPending] = useState(false);
  const [mutationPending, setMutationPending] = useState(false);
  const [operationError, setOperationError] = useState<string | null>(null);
  const [operationFeedback, setOperationFeedback] = useState<string | null>(null);
  const [clock, setClock] = useState(() => Date.now());
  const resendDeadline = accountSettings?.email_resend_available_at
    ? Date.parse(accountSettings.email_resend_available_at)
    : Number.NaN;
  const resendCooldownSeconds = Number.isFinite(resendDeadline)
    ? Math.max(0, Math.ceil((resendDeadline - clock) / 1000))
    : 0;
  const [editMode, setEditMode] = useState<EditMode>(null);
  const [emailStep, setEmailStep] = useState<EmailStep>("address");
  const [usernameDraft, setUsernameDraft] = useState("");
  const [usernameCurrentPassword, setUsernameCurrentPassword] = useState("");
  const [emailDraft, setEmailDraft] = useState("");
  const [emailCurrentPassword, setEmailCurrentPassword] = useState("");
  const [emailCode, setEmailCode] = useState("");
  const [passwords, setPasswords] = useState(EMPTY_PASSWORDS);
  const [touched, setTouched] = useState(EMPTY_TOUCHED_FIELDS);
  const usernameInputRef = useRef<HTMLInputElement>(null);
  const usernamePasswordInputRef = useRef<HTMLInputElement>(null);
  const emailInputRef = useRef<HTMLInputElement>(null);
  const emailPasswordInputRef = useRef<HTMLInputElement>(null);
  const emailCodeInputRef = useRef<HTMLInputElement>(null);
  const currentPasswordInputRef = useRef<HTMLInputElement>(null);
  const newPasswordInputRef = useRef<HTMLInputElement>(null);
  const repeatPasswordInputRef = useRef<HTMLInputElement>(null);
  const usernameEditButtonRef = useRef<HTMLButtonElement>(null);
  const emailEditButtonRef = useRef<HTMLButtonElement>(null);
  const passwordEditButtonRef = useRef<HTMLButtonElement>(null);
  const operationControllerRef = useRef<AbortController | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;

    void (async () => {
      const result = await playerModel.restoreCurrentPlayer(controller.signal);
      if (cancelled || result.kind === "aborted") return;

      if (result.kind === "ok") {
        const settingsResult = await playerModel.getAccountSettings(controller.signal);
        if (cancelled || settingsResult.kind === "aborted") return;

        if (settingsResult.kind === "ok") {
          setPlayer(result.state.player);
          setAccountSettings(settingsResult.settings);
          if (settingsResult.settings.pending_email) {
            setEmailDraft(settingsResult.settings.pending_email);
          }
          setRestoreState("ready");
        } else if (settingsResult.kind === "expired") {
          setPlayer(null);
          setAccountSettings(null);
          setRestoreState("ready");
        } else {
          setPlayer(null);
          setAccountSettings(null);
          setRestoreState("error");
        }
      } else {
        setPlayer(null);
        setAccountSettings(null);
        setRestoreState(result.kind === "error" || result.kind === "contract" ? "error" : "ready");
      }
    })();

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [restoreAttempt]);

  useEffect(() => () => {
    operationControllerRef.current?.abort();
    operationControllerRef.current = null;
  }, []);

  useEffect(() => {
    if (!accountSettings?.pending_email || !accountSettings.email_resend_available_at) {
      return undefined;
    }
    const timer = window.setInterval(() => setClock(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [accountSettings?.email_resend_available_at, accountSettings?.pending_email]);

  useLayoutEffect(() => {
    if (!editMode) return undefined;

    if (editMode === "username") usernameInputRef.current?.focus();
    if (editMode === "email" && emailStep === "address") emailInputRef.current?.focus();
    if (editMode === "email" && emailStep === "code") emailCodeInputRef.current?.focus();
    if (editMode === "password") currentPasswordInputRef.current?.focus();
  }, [editMode, emailStep]);

  const clearForms = useCallback(() => {
    setEditMode(null);
    setEmailStep("address");
    setUsernameDraft("");
    setUsernameCurrentPassword("");
    setEmailDraft("");
    setEmailCurrentPassword("");
    setEmailCode("");
    setPasswords({ ...EMPTY_PASSWORDS });
    setTouched({ ...EMPTY_TOUCHED_FIELDS });
    setOperationError(null);
    setOperationFeedback(null);
  }, []);

  const abortAccountOperation = useCallback(() => {
    operationControllerRef.current?.abort();
    operationControllerRef.current = null;
    setMutationPending(false);
  }, []);

  useEffect(() => {
    if (previousSectionRef.current === activeSection) return;
    previousSectionRef.current = activeSection;
    abortAccountOperation();
    clearForms();
  }, [abortAccountOperation, activeSection, clearForms]);

  const startAccountOperation = (): AbortController | null => {
    if (!player || operationControllerRef.current) return null;
    const controller = new AbortController();
    operationControllerRef.current = controller;
    setMutationPending(true);
    setOperationError(null);
    setOperationFeedback(null);
    return controller;
  };

  const finishAccountOperation = (controller: AbortController) => {
    if (operationControllerRef.current === controller) {
      operationControllerRef.current = null;
      setMutationPending(false);
    }
  };

  const handleExpiredSession = useCallback(() => {
    setNotificationsEnabled(false);
    abortAccountOperation();
    void playerModel.clearCurrentPlayer();
    clearForms();
    setPlayer(null);
    setAccountSettings(null);
    setRestoreState("ready");
  }, [abortAccountOperation, clearForms, setNotificationsEnabled]);

  useEffect(() => {
    if (player && notificationStatus === "signed_out") {
      abortAccountOperation();
      clearForms();
      setPlayer(null);
      setAccountSettings(null);
      setRestoreState("ready");
    }
  }, [abortAccountOperation, clearForms, notificationStatus, player]);

  const handleLogout = async () => {
    if (!player || logoutPending) return;
    setNotificationsEnabled(false);
    abortAccountOperation();
    setLogoutPending(true);
    await playerModel.clearCurrentPlayer();
    clearForms();
    setPlayer(null);
    setAccountSettings(null);
    setLogoutPending(false);
  };

  useSiteHeaderAuth(player ? handleLogout : undefined, logoutPending);

  const usernameError = touched.username && !isValidUsername(usernameDraft.trim())
    ? USERNAME_ERROR
    : null;
  const usernameCurrentPasswordError = touched.usernameCurrentPassword && !usernameCurrentPassword
    ? "Введите текущий пароль."
    : null;
  const emailError = touched.email ? settingsEmailError(emailDraft) : null;
  const emailCurrentPasswordError = touched.emailCurrentPassword && !emailCurrentPassword
    ? "Введите текущий пароль."
    : null;
  const emailCodeError = touched.emailCode ? settingsEmailCodeError(emailCode) : null;
  const currentPasswordError = touched.currentPassword && !passwords.current
    ? "Введите текущий пароль."
    : null;
  const newPasswordError = touched.newPassword ? settingsPasswordError(passwords.next) : null;
  const repeatPasswordError = touched.repeatPassword
    ? !passwords.repeat
      ? "Повторите пароль."
      : passwords.repeat !== passwords.next
        ? "Пароли не совпадают."
        : null
    : null;

  const changeSection = (section: SettingsSection) => {
    abortAccountOperation();
    clearForms();
    const nextParams = new URLSearchParams(searchParamsString);
    if (section === "profile") {
      nextParams.delete("section");
    } else {
      nextParams.set("section", section);
    }
    const nextQuery = nextParams.toString();
    if (nextQuery === searchParamsString) return;
    router.push(nextQuery ? `/settings?${nextQuery}` : "/settings", { scroll: false });
  };

  const cancelEditor = () => {
    const previousMode = editMode;
    clearForms();
    if (previousMode === "username") usernameEditButtonRef.current?.focus();
    if (previousMode === "email") emailEditButtonRef.current?.focus();
    if (previousMode === "password") passwordEditButtonRef.current?.focus();
  };

  const retryRestore = () => {
    setAccountSettings(null);
    setPlayer(null);
    setRestoreState("loading");
    setRestoreAttempt((current) => current + 1);
  };

  const openUsernameEditor = () => {
    if (!player) return;
    clearForms();
    setUsernameDraft(player.username);
    setEditMode("username");
  };

  const openEmailEditor = () => {
    if (!player) return;
    clearForms();
    if (accountSettings?.pending_email) {
      setEmailDraft(accountSettings.pending_email);
      setEmailStep("code");
    }
    setEditMode("email");
  };

  const touch = (field: keyof TouchedFields) => {
    setTouched((current) => ({ ...current, [field]: true }));
  };

  const showMutationError = (result: Exclude<AccountMutationResult, { kind: "ok" }>) => {
    if (result.kind === "expired") {
      handleExpiredSession();
      return;
    }
    if (result.kind === "current_password_invalid") {
      setOperationError(CURRENT_PASSWORD_ERROR);
    } else if (result.kind === "username_taken") {
      setOperationError(USERNAME_TAKEN_ERROR);
    } else if (result.kind === "email_taken") {
      setOperationError(EMAIL_TAKEN_ERROR);
    } else if (result.kind === "password_invalid") {
      setOperationError("Новый пароль не соответствует требованиям.");
    } else if (result.kind === "code_invalid") {
      setOperationError("Код неверный или срок его действия истек.");
    } else if (result.kind === "rate_limited") {
      setOperationError("Слишком много попыток. Попробуйте позже.");
    } else if (result.kind === "unavailable") {
      setOperationError(ACCOUNT_UNAVAILABLE_ERROR);
    } else if (result.kind === "error") {
      setOperationError(ACCOUNT_UNAVAILABLE_ERROR);
    }
  };

  const handleUsernameSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    touch("username");
    touch("usernameCurrentPassword");
    if (!isValidUsername(usernameDraft.trim())) usernameInputRef.current?.focus();
    else if (usernameCurrentPassword.length === 0) usernamePasswordInputRef.current?.focus();
    else {
      const controller = startAccountOperation();
      if (!controller) return;
      try {
      const result = await playerModel.changeUsername(
          usernameCurrentPassword,
          usernameDraft.trim(),
          controller.signal,
      );
      if (controller.signal.aborted || operationControllerRef.current !== controller) return;
      setUsernameCurrentPassword("");
      setTouched((current) => ({ ...current, usernameCurrentPassword: false }));
      if (result.kind === "ok" && result.player) {
          setPlayer(result.player);
          setAccountSettings((current) => current
            ? { ...current, username: result.player!.username }
            : current);
          clearForms();
          setOperationFeedback("Логин изменен.");
        } else if (result.kind !== "aborted" && result.kind !== "ok") {
          showMutationError(result);
        }
      } finally {
        finishAccountOperation(controller);
      }
    }
  };

  const handleEmailSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    touch("email");
    touch("emailCurrentPassword");
    if (settingsEmailError(emailDraft) !== null) {
      emailInputRef.current?.focus();
      return;
    }
    if (emailCurrentPassword.length === 0) {
      emailPasswordInputRef.current?.focus();
      return;
    }

    const controller = startAccountOperation();
    if (!controller) return;
    try {
      const result = await playerModel.beginEmailChange(
        emailCurrentPassword,
        emailDraft.trim(),
        controller.signal,
      );
      if (controller.signal.aborted || operationControllerRef.current !== controller) return;
      setEmailCurrentPassword("");
      setTouched((current) => ({ ...current, emailCurrentPassword: false }));
      if (result.kind === "ok" && result.settings) {
        setAccountSettings(result.settings);
        setEmailDraft(result.settings.pending_email ?? emailDraft.trim());
        setEmailCode("");
        setEmailStep("code");
        setTouched((current) => ({ ...current, emailCode: false }));
        setOperationFeedback("Код отправлен на новый адрес.");
      } else if (result.kind !== "aborted" && result.kind !== "ok") {
        showMutationError(result);
      }
    } finally {
      finishAccountOperation(controller);
    }
  };

  const handleEmailCodeSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    touch("emailCode");
    if (settingsEmailCodeError(emailCode) !== null) {
      emailCodeInputRef.current?.focus();
      return;
    }
    const controller = startAccountOperation();
    if (!controller) return;
    try {
      const result = await playerModel.confirmEmailChange(emailCode, controller.signal);
      if (controller.signal.aborted || operationControllerRef.current !== controller) return;
      setEmailCode("");
      setTouched((current) => ({ ...current, emailCode: false }));
      if (result.kind === "ok") {
        setAccountSettings((current) => current
          ? {
            ...current,
            email: result.confirmation.email,
            pending_email: null,
            pending_email_expires_at: null,
            email_resend_available_at: null,
          }
          : current);
        clearForms();
        setOperationFeedback(result.confirmation.previous_email_notified
          ? "Email обновлен."
          : "Email обновлен, но письмо на прежний адрес отправить не удалось.");
      } else if (result.kind !== "aborted") {
        showMutationError(result);
      }
    } finally {
      finishAccountOperation(controller);
    }
  };

  const resendEmailCode = async () => {
    if (!accountSettings?.pending_email || resendCooldownSeconds > 0) return;
    const controller = startAccountOperation();
    if (!controller) return;
    try {
      const result = await playerModel.resendEmailChange(controller.signal);
      if (controller.signal.aborted || operationControllerRef.current !== controller) return;
      if (result.kind === "ok" && result.settings) {
        setAccountSettings(result.settings);
        setOperationFeedback("Код отправлен повторно.");
      } else if (result.kind === "rate_limited") {
        const retrySeconds = parseRetryAfterSeconds(result.retryAfter) ?? 60;
        setAccountSettings((current) => current
          ? { ...current, email_resend_available_at: new Date(Date.now() + retrySeconds * 1000).toISOString() }
          : current);
        showMutationError(result);
      } else if (result.kind !== "aborted" && result.kind !== "ok") {
        showMutationError(result);
      }
    } finally {
      finishAccountOperation(controller);
    }
  };

  const changeEmailAddress = async () => {
    if (!accountSettings?.pending_email) {
      setEmailStep("address");
      setEmailCurrentPassword("");
      setEmailCode("");
      emailInputRef.current?.focus();
      return;
    }
    const addressToKeep = emailDraft;
    const controller = startAccountOperation();
    if (!controller) return;
    try {
      const result = await playerModel.cancelEmailChange(controller.signal);
      if (controller.signal.aborted || operationControllerRef.current !== controller) return;
      if (result.kind === "ok" && result.settings) {
        setAccountSettings(result.settings);
        setEmailDraft(addressToKeep);
        setEmailStep("address");
        setEmailCurrentPassword("");
        setEmailCode("");
        setTouched((current) => ({ ...current, emailCurrentPassword: false, emailCode: false }));
        emailInputRef.current?.focus();
      } else if (result.kind !== "aborted" && result.kind !== "ok") {
        showMutationError(result);
      }
    } finally {
      finishAccountOperation(controller);
    }
  };

  const handlePasswordSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    touch("currentPassword");
    touch("newPassword");
    touch("repeatPassword");
    if (!passwords.current) {
      currentPasswordInputRef.current?.focus();
      return;
    }
    if (settingsPasswordError(passwords.next) !== null) {
      newPasswordInputRef.current?.focus();
      return;
    }
    if (!passwords.repeat || passwords.repeat !== passwords.next) {
      repeatPasswordInputRef.current?.focus();
      return;
    }
    const controller = startAccountOperation();
    if (!controller) return;
    try {
      const result = await playerModel.changePassword(passwords.current, passwords.next, controller.signal);
      if (controller.signal.aborted || operationControllerRef.current !== controller) return;
      setPasswords({ ...EMPTY_PASSWORDS });
      setTouched((current) => ({
        ...current,
        currentPassword: false,
        newPassword: false,
        repeatPassword: false,
      }));
      if (result.kind === "ok") {
        clearForms();
        setOperationFeedback("Пароль изменен.");
      } else if (result.kind !== "aborted") {
        showMutationError(result);
      }
    } finally {
      finishAccountOperation(controller);
    }
  };

  const handleFieldBlur = (
    field: keyof TouchedFields,
    event: ReactFocusEvent<HTMLInputElement>,
  ) => {
    const nextFocus = event.relatedTarget;
    if (
      nextFocus instanceof HTMLElement &&
      nextFocus.closest("[data-settings-reset-editor], [data-settings-submit-editor]")
    ) {
      return;
    }
    touch(field);
  };

  return (
    <main className={styles.page}>
      <div className={styles.shell}>
        <header className={styles.pageHeader}>
          <div>
            <p className={styles.eyebrow}>АККАУНТ</p>
            <h1 className={styles.pageTitle}>Настройки</h1>
            <p className={styles.pageSubtitle}>Профиль и параметры интерфейса.</p>
          </div>
        </header>

        <div className={styles.layout}>
          <nav className={styles.navigation} aria-label="Разделы настроек">
            {sectionLabels.map((section) => (
              <button
                key={section.id}
                type="button"
                className={styles.navigationButton}
                data-settings-reset-editor
                aria-pressed={activeSection === section.id}
                onClick={() => changeSection(section.id)}
              >
                {section.label}
              </button>
            ))}
          </nav>

          <div className={styles.content}>
            <section
              aria-labelledby={`${idPrefix}-profile-title`}
              hidden={activeSection !== "profile"}
            >
                <header className={styles.sectionHeader}>
                  <h2 className={styles.sectionTitle} id={`${idPrefix}-profile-title`}>Профиль</h2>
                  <p className={styles.sectionDescription}>Ваши данные аккаунта.</p>
                </header>

                {operationError ? <p className={styles.errorText} role="alert">{operationError}</p> : null}
                {operationFeedback ? <p className={styles.statusMessage} role="status">{operationFeedback}</p> : null}

                {player ? (
                  <div className={`${styles.rows} ${styles.rowsAfterAvatar}`}>
                    <AvatarEditor username={player.username} />
                    <div className={styles.settingRow}>
                      <div className={styles.rowCopy}>
                        <h3>Логин</h3>
                        <p>Имя, которое отображается в рейтинге.</p>
                      </div>
                      <div className={styles.rowValue}>
                        <strong>{player.username}</strong>
                        <button
                          ref={usernameEditButtonRef}
                          type="button"
                          className={styles.textButton}
                          aria-label={editMode === "username" ? "Свернуть форму изменения логина" : "Изменить логин"}
                          aria-expanded={editMode === "username"}
                          aria-controls={editMode === "username" ? `${idPrefix}-username-editor` : undefined}
                          data-settings-reset-editor
                          disabled={mutationPending}
                          onClick={() => {
                            if (editMode === "username") cancelEditor();
                            else openUsernameEditor();
                          }}
                        >
                          Изменить
                        </button>
                      </div>
                    </div>
                    {editMode === "username" ? (
                      <form id={`${idPrefix}-username-editor`} className={styles.editor} noValidate onSubmit={handleUsernameSubmit}>
                        <div className={styles.field}>
                          <label className={styles.label} htmlFor={`${idPrefix}-username`}>Новый логин</label>
                          <input
                            id={`${idPrefix}-username`}
                            ref={usernameInputRef}
                            className={styles.input}
                            type="text"
                            autoComplete="username"
                            autoCapitalize="none"
                            spellCheck={false}
                            value={usernameDraft}
                            aria-invalid={usernameError ? true : undefined}
                            aria-describedby={usernameError ? `${idPrefix}-username-hint ${idPrefix}-username-error` : `${idPrefix}-username-hint`}
                            onChange={(event) => {
                              setUsernameDraft(event.target.value);
                              setOperationError(null);
                              setOperationFeedback(null);
                            }}
                            onBlur={(event) => handleFieldBlur("username", event)}
                          />
                          <p className={styles.hint} id={`${idPrefix}-username-hint`}>
                            2-50 латинских букв, цифр, _ или -.
                          </p>
                          {usernameError ? <p className={styles.errorText} id={`${idPrefix}-username-error`}>{usernameError}</p> : null}
                        </div>
                        <PasswordField
                          id={`${idPrefix}-username-current-password`}
                          inputRef={usernamePasswordInputRef}
                          label="Текущий пароль"
                          value={usernameCurrentPassword}
                          autoComplete="current-password"
                          error={usernameCurrentPasswordError}
                          onChange={setUsernameCurrentPassword}
                          onBlur={(event) => handleFieldBlur("usernameCurrentPassword", event)}
                        />
                        <div className={styles.formActions}>
                          <button type="button" className={styles.secondaryButton} data-settings-reset-editor onClick={cancelEditor}>Отмена</button>
                          <button
                            type="submit"
                            className={styles.primaryButton}
                            data-settings-submit-editor
                            disabled={mutationPending}
                          >
                            {mutationPending ? "Сохраняем..." : "Изменить логин"}
                          </button>
                        </div>
                      </form>
                    ) : null}

                    <div className={styles.settingRow}>
                      <div className={styles.rowCopy}>
                        <h3>Email</h3>
                        <p>Адрес для писем аккаунта.</p>
                      </div>
                      <div className={styles.rowValue}>
                        {accountSettings?.email ? <strong>{accountSettings.email}</strong> : null}
                        <button
                          ref={emailEditButtonRef}
                          type="button"
                          className={styles.textButton}
                          aria-label="Изменить email"
                          aria-expanded={editMode === "email"}
                          aria-controls={editMode === "email" ? `${idPrefix}-email-editor` : undefined}
                          data-settings-reset-editor
                          disabled={mutationPending}
                          onClick={() => {
                            if (editMode === "email") cancelEditor();
                            else openEmailEditor();
                          }}
                        >
                          Изменить
                        </button>
                      </div>
                    </div>
                    {editMode === "email" ? (
                      emailStep === "address" ? (
                        <form id={`${idPrefix}-email-editor`} className={styles.editor} noValidate onSubmit={handleEmailSubmit}>
                          <div className={styles.field}>
                            <label className={styles.label} htmlFor={`${idPrefix}-email`}>Новый email</label>
                            <input
                              id={`${idPrefix}-email`}
                              ref={emailInputRef}
                              className={styles.input}
                              type="email"
                              autoComplete="email"
                              value={emailDraft}
                              aria-invalid={emailError ? true : undefined}
                              aria-describedby={emailError ? `${idPrefix}-email-hint ${idPrefix}-email-error` : `${idPrefix}-email-hint`}
                              onChange={(event) => {
                                setEmailDraft(event.target.value);
                                setOperationError(null);
                                setOperationFeedback(null);
                              }}
                              onBlur={(event) => handleFieldBlur("email", event)}
                            />
                            <p className={styles.hint} id={`${idPrefix}-email-hint`}>
                              Новый адрес нужно будет подтвердить кодом из письма.
                            </p>
                            {emailError ? <p className={styles.errorText} id={`${idPrefix}-email-error`}>{emailError}</p> : null}
                          </div>
                          <PasswordField
                            id={`${idPrefix}-email-current-password`}
                            inputRef={emailPasswordInputRef}
                            label="Текущий пароль"
                            value={emailCurrentPassword}
                            autoComplete="current-password"
                            error={emailCurrentPasswordError}
                            onChange={setEmailCurrentPassword}
                            onBlur={(event) => handleFieldBlur("emailCurrentPassword", event)}
                          />
                          <div className={styles.formActions}>
                            <button type="button" className={styles.secondaryButton} data-settings-reset-editor disabled={mutationPending} onClick={cancelEditor}>Отмена</button>
                            <button
                              type="submit"
                              className={styles.primaryButton}
                              data-settings-submit-editor
                              disabled={mutationPending}
                            >
                              {mutationPending ? "Отправляем..." : "Продолжить"}
                            </button>
                          </div>
                        </form>
                      ) : (
                        <form id={`${idPrefix}-email-editor`} className={styles.editor} noValidate onSubmit={handleEmailCodeSubmit}>
                          <div className={styles.pendingAddress}>
                            <div className={styles.pendingAddressValue}>
                              <span className={styles.pendingAddressLabel}>Новый адрес</span>
                              <strong>{emailDraft.trim()}</strong>
                            </div>
                          <button type="button" className={styles.textButton} data-settings-reset-editor disabled={mutationPending} onClick={changeEmailAddress}>
                            Изменить адрес
                            </button>
                          </div>
                          <div className={styles.field}>
                            <label className={styles.label} htmlFor={`${idPrefix}-email-code`}>Код подтверждения</label>
                            <input
                              id={`${idPrefix}-email-code`}
                              ref={emailCodeInputRef}
                              className={styles.input}
                              type="text"
                              inputMode="numeric"
                              autoComplete="one-time-code"
                              autoCapitalize="none"
                              spellCheck={false}
                              maxLength={6}
                              pattern="[0-9]{6}"
                              value={emailCode}
                              aria-invalid={emailCodeError ? true : undefined}
                              aria-describedby={emailCodeError ? `${idPrefix}-email-code-hint ${idPrefix}-email-code-error` : `${idPrefix}-email-code-hint`}
                              onChange={(event) => {
                                setEmailCode(event.target.value.replace(/[^0-9]/gu, "").slice(0, 6));
                                setOperationError(null);
                                setOperationFeedback(null);
                              }}
                              onPaste={(event) => {
                                event.preventDefault();
                                setEmailCode(event.clipboardData.getData("text").replace(/[^0-9]/gu, "").slice(0, 6));
                              }}
                              onBlur={(event) => handleFieldBlur("emailCode", event)}
                            />
                            <p className={styles.hint} id={`${idPrefix}-email-code-hint`}>{EMAIL_CODE_HINT}</p>
                            <p className={styles.hint}>Код действует 10 минут.</p>
                            {emailCodeError ? <p className={styles.errorText} id={`${idPrefix}-email-code-error`}>{emailCodeError}</p> : null}
                          </div>
                          <button
                            type="button"
                            className={`${styles.textButton} ${styles.codeResendButton}`}
                            disabled={mutationPending || resendCooldownSeconds > 0 || !accountSettings?.pending_email}
                            onClick={() => void resendEmailCode()}
                          >
                            {mutationPending ? "Отправляем код..." : "Отправить код повторно"}
                          </button>
                          {resendCooldownSeconds > 0 ? (
                            <p className={styles.hint}>Повторная отправка доступна через {resendCooldownSeconds} с.</p>
                          ) : null}
                          <div className={styles.formActions}>
                            <button type="button" className={styles.secondaryButton} data-settings-reset-editor disabled={mutationPending} onClick={cancelEditor}>Отмена</button>
                            <button type="submit" className={styles.primaryButton} data-settings-submit-editor disabled={mutationPending}>
                              {mutationPending ? "Проверяем..." : "Подтвердить почту"}
                            </button>
                          </div>
                        </form>
                      )
                    ) : null}
                  </div>
                ) : (
                  <AccountAccessNotice player={player} restoreState={restoreState} onRetry={retryRestore} />
                )}
            </section>

            {activeSection === "security" ? (
              <section aria-labelledby={`${idPrefix}-security-title`}>
                <header className={styles.sectionHeader}>
                  <h2 className={styles.sectionTitle} id={`${idPrefix}-security-title`}>Безопасность</h2>
                  <p className={styles.sectionDescription}>Пароль и способы защиты аккаунта.</p>
                </header>

                {operationError ? <p className={styles.errorText} role="alert">{operationError}</p> : null}
                {operationFeedback ? <p className={styles.statusMessage} role="status">{operationFeedback}</p> : null}

                {player ? (
                  <>
                    <div className={styles.rows}>
                      <div className={styles.settingRow}>
                        <div className={styles.rowCopy}>
                          <h3>Пароль</h3>
                          <p>Обновите пароль для входа в аккаунт.</p>
                        </div>
                        <button
                          ref={passwordEditButtonRef}
                          type="button"
                          className={styles.textButton}
                          aria-label={editMode === "password" ? "Свернуть форму изменения пароля" : undefined}
                          aria-expanded={editMode === "password"}
                          aria-controls={editMode === "password" ? `${idPrefix}-password-editor` : undefined}
                          data-settings-reset-editor
                          disabled={mutationPending}
                          onClick={() => {
                            if (editMode === "password") cancelEditor();
                            else {
                              clearForms();
                              setEditMode("password");
                            }
                          }}
                        >
                          Изменить пароль
                        </button>
                      </div>
                      {editMode === "password" ? (
                        <form id={`${idPrefix}-password-editor`} className={styles.passwordForm} noValidate onSubmit={handlePasswordSubmit}>
                          <h3 className={styles.formTitle}>Смена пароля</h3>
                          <PasswordField
                            id={`${idPrefix}-current-password`}
                            label="Текущий пароль"
                            value={passwords.current}
                            autoComplete="current-password"
                            error={currentPasswordError}
                            inputRef={currentPasswordInputRef}
                            onChange={(value) => {
                              setPasswords((current) => ({ ...current, current: value }));
                              setOperationError(null);
                              setOperationFeedback(null);
                            }}
                            onBlur={(event) => handleFieldBlur("currentPassword", event)}
                          />
                          <PasswordField
                            id={`${idPrefix}-new-password`}
                            label="Новый пароль"
                            value={passwords.next}
                            autoComplete="new-password"
                            hint={PASSWORD_HINT}
                            error={newPasswordError}
                            onChange={(value) => {
                              setPasswords((current) => ({ ...current, next: value }));
                              setOperationError(null);
                              setOperationFeedback(null);
                            }}
                            onBlur={(event) => handleFieldBlur("newPassword", event)}
                          />
                          <PasswordField
                            id={`${idPrefix}-repeat-password`}
                            label="Повторите новый пароль"
                            value={passwords.repeat}
                            autoComplete="new-password"
                            error={repeatPasswordError}
                            onChange={(value) => {
                              setPasswords((current) => ({ ...current, repeat: value }));
                              setOperationError(null);
                              setOperationFeedback(null);
                            }}
                            onBlur={(event) => handleFieldBlur("repeatPassword", event)}
                          />
                          <div className={styles.formActions}>
                            <button type="button" className={styles.secondaryButton} data-settings-reset-editor disabled={mutationPending} onClick={cancelEditor}>Отмена</button>
                            <button
                              type="submit"
                              className={styles.primaryButton}
                              data-settings-submit-editor
                              disabled={mutationPending}
                            >
                              {mutationPending ? "Сохраняем..." : "Изменить пароль"}
                            </button>
                          </div>
                        </form>
                      ) : null}
                    </div>

                    <div className={styles.futureRows}>
                      <div className={styles.futureRow}>
                        <div className={styles.rowCopy}>
                          <h3>Двухфакторная защита</h3>
                        </div>
                        <div className={styles.futureActions}>
                          <button type="button" className={styles.secondaryButton} disabled>Настроить</button>
                        </div>
                      </div>
                      <div className={styles.futureRow}>
                        <div className={styles.rowCopy}>
                          <h3>Сеансы</h3>
                        </div>
                        <div className={styles.futureActions}>
                          <button type="button" className={styles.secondaryButton} disabled>Управлять</button>
                        </div>
                      </div>
                    </div>
                  </>
                ) : (
                  <AccountAccessNotice player={player} restoreState={restoreState} onRetry={retryRestore} />
                )}
              </section>
            ) : null}

            {activeSection === "appearance" ? (
              <section aria-labelledby={`${idPrefix}-appearance-title`}>
                <header className={styles.sectionHeader}>
                  <h2 className={styles.sectionTitle} id={`${idPrefix}-appearance-title`}>Оформление</h2>
                </header>
                <div className={styles.rows}>
                  <div className={styles.settingRow}>
                    <div className={styles.rowCopy}>
                      <h3>Тема</h3>
                    </div>
                    <ThemeToggle storageKey={THEME_STORAGE_KEY} variant="menu" />
                  </div>
                  <div className={styles.settingRow}>
                    <div className={styles.rowCopy}>
                      <h3>Язык</h3>
                    </div>
                    <button
                      type="button"
                      className={styles.secondaryButton}
                      disabled
                    >
                      Русский
                    </button>
                  </div>
                </div>
              </section>
            ) : null}

            {activeSection === "notifications" ? (
              <section aria-labelledby={`${idPrefix}-notifications-title`}>
                <header className={styles.sectionHeader}>
                  <h2 className={styles.sectionTitle} id={`${idPrefix}-notifications-title`}>Уведомления</h2>
                </header>
                {!player ? (
                  <AccountAccessNotice player={player} restoreState={restoreState} onRetry={retryRestore} />
                ) : logoutPending ? (
                  <p className={styles.statusMessage} role="status">Завершаем сеанс...</p>
                ) : (
                  <div aria-busy={notifications.status === "checking"}>
                    {notifications.status === "checking" ? (
                      <p className={styles.statusMessage} role="status">Загрузка уведомлений...</p>
                    ) : null}
                    {notifications.status === "signed_out" ? (
                      <div className={styles.notice} role="alert">
                        <p>Сессия завершена. Войдите снова.</p>
                        <Link href="/login" className={styles.loginLink}>Войти</Link>
                      </div>
                    ) : null}
                    {notifications.status === "forbidden" ? (
                      <p className={styles.errorText} role="alert">Нет доступа к уведомлениям.</p>
                    ) : null}
                    {notifications.status === "error" ? (
                      <div className={styles.notice} role="alert">
                        <p>Не удалось обновить уведомления.</p>
                        <button type="button" className={styles.textButton} onClick={notifications.refresh}>
                          Повторить
                        </button>
                      </div>
                    ) : null}
                    {notifications.notifications.length > 0 ? (
                      <ol className={styles.notificationHistoryList} aria-label="История уведомлений">
                        {notifications.notifications.map((notification: PlayerNotification) => (
                          <li key={notification.id} className={styles.notificationHistoryItem}>
                            <p>
                              Администратор удалил вас из соревнования &quot;{notification.tournament_name}&quot;.
                            </p>
                            <time dateTime={notification.created_at}>
                              {notificationDate(notification.created_at)}
                            </time>
                          </li>
                        ))}
                      </ol>
                    ) : notifications.status === "ready" ? (
                      <p className={styles.statusMessage}>За последние 24 часа уведомлений нет.</p>
                    ) : null}
                  </div>
                )}
              </section>
            ) : null}
          </div>
        </div>
      </div>
    </main>
  );
}
