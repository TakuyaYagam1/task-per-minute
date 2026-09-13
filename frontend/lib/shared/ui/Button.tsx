import {
  forwardRef,
  type ButtonHTMLAttributes,
  type ReactNode,
} from "react";

import styles from "./Button.module.css";

export type ButtonVariant =
  | "primary"
  | "secondary"
  | "success"
  | "danger"
  | "ghost";

export type ButtonSize = "small" | "medium" | "large";

export interface ButtonProps
  extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children"> {
  children: ReactNode;
  /** Shows an explicit in-progress state and prevents duplicate activation. */
  loading?: boolean;
  /** Accessible text announced while loading. */
  loadingLabel?: string;
  variant?: ButtonVariant;
  size?: ButtonSize;
}

const joinClasses = (...classes: Array<string | false | null | undefined>) =>
  classes.filter(Boolean).join(" ");

/**
 * Shared action button used throughout tournament surfaces.
 *
 * The old `primary`, `secondary`, `success`, `danger` and size values remain
 * valid. Native button attributes are forwarded so forms and keyboard users
 * keep their expected browser behaviour.
 */
export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  (
    {
      children,
      className,
      disabled = false,
      loading = false,
      loadingLabel = "Загрузка",
      size = "medium",
      type = "button",
      variant = "primary",
      "aria-busy": ariaBusy,
      ...buttonProps
    },
    ref,
  ) => {
    const isDisabled = disabled || loading;

    return (
      <button
        {...buttonProps}
        ref={ref}
        type={type}
        disabled={isDisabled}
        aria-busy={loading || ariaBusy ? true : undefined}
        className={joinClasses(
          styles.button,
          styles[`variant-${variant}`],
          styles[`size-${size}`],
          loading && styles.loading,
          isDisabled && styles.disabled,
          className,
        )}
      >
        {loading ? (
          <>
            <span className={styles.spinner} aria-hidden="true" />
            <span>{loadingLabel}</span>
            <span className={styles.srOnly}>{children}</span>
          </>
        ) : (
          children
        )}
      </button>
    );
  },
);

Button.displayName = "Button";
