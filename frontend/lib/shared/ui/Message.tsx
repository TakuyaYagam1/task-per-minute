import { type HTMLAttributes, type ReactNode } from "react";

import styles from "./TournamentUi.module.css";

export type MessageTone =
  | "info"
  | "success"
  | "warning"
  | "error"
  | "loading"
  | "empty"
  | "neutral";

const MESSAGE_TITLES: Record<MessageTone, string> = {
  info: "Информация",
  success: "Готово",
  warning: "Внимание",
  error: "Ошибка",
  loading: "Загрузка",
  empty: "Нет данных",
  neutral: "Сообщение",
};

const MESSAGE_MARKS: Record<MessageTone, string> = {
  info: "i",
  success: "v",
  warning: "!",
  error: "!",
  loading: "",
  empty: "-",
  neutral: "-",
};

export interface MessageProps extends Omit<HTMLAttributes<HTMLDivElement>, "title"> {
  children?: ReactNode;
  title?: ReactNode;
  tone?: MessageTone;
  onDismiss?: () => void;
  dismissLabel?: string;
}

export const Message = ({
  children,
  className,
  dismissLabel = "Закрыть сообщение",
  onDismiss,
  title,
  tone = "info",
  ...messageProps
}: MessageProps) => {
  const isError = tone === "error";
  const heading = title ?? MESSAGE_TITLES[tone];

  return (
    <div
      {...messageProps}
      className={[styles.message, styles[`message-${tone}`], className]
        .filter(Boolean)
        .join(" ")}
      role={messageProps.role ?? (isError ? "alert" : "status")}
      aria-live={messageProps["aria-live"] ?? (isError ? "assertive" : "polite")}
      aria-atomic={messageProps["aria-atomic"] ?? true}
    >
      <span className={styles.messageMark} aria-hidden="true">
        {tone === "loading" ? <span className={styles.statusSpinner} /> : MESSAGE_MARKS[tone]}
      </span>
      <div className={styles.messageContent}>
        <strong className={styles.messageTitle}>{heading}</strong>
        {children !== undefined && <div className={styles.messageBody}>{children}</div>}
      </div>
      {onDismiss && (
        <button
          type="button"
          className={styles.messageDismiss}
          aria-label={dismissLabel}
          onClick={onDismiss}
        >
          <span aria-hidden="true">x</span>
        </button>
      )}
    </div>
  );
};

Message.displayName = "Message";
