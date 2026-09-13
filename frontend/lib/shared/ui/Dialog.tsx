"use client";

import {
  forwardRef,
  useCallback,
  useEffect,
  useId,
  useRef,
  type DialogHTMLAttributes,
  type ForwardedRef,
  type KeyboardEvent,
  type MouseEvent,
  type MutableRefObject,
  type ReactNode,
  type SyntheticEvent,
} from "react";

import styles from "./TournamentUi.module.css";

export type DialogSize = "small" | "medium" | "large";

export interface DialogProps
  extends Omit<
    DialogHTMLAttributes<HTMLDialogElement>,
    "children" | "open" | "onClose" | "onCancel" | "title"
  > {
  children?: ReactNode;
  open?: boolean;
  /** `isOpen` is accepted as an alias for controlled call sites. */
  isOpen?: boolean;
  title?: ReactNode;
  description?: ReactNode;
  footer?: ReactNode;
  size?: DialogSize;
  onOpenChange?: (open: boolean) => void;
  onClose?: () => void;
  onCancel?: () => void;
  closeOnEscape?: boolean;
  closeOnBackdrop?: boolean;
  /** Alias for `closeOnBackdrop`. */
  closeOnOverlayClick?: boolean;
  showCloseButton?: boolean;
  closeLabel?: string;
  initialFocusRef?: { current: HTMLElement | null };
  returnFocusRef?: { current: HTMLElement | null };
}

const FOCUSABLE_SELECTOR = [
  "a[href]",
  "area[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  "iframe",
  "object",
  "embed",
  "[contenteditable]",
  "[tabindex]:not([tabindex=\"-1\"])",
].join(",");

const assignRef = <Value,>(
  ref: ForwardedRef<Value>,
  value: Value | null,
) => {
  if (typeof ref === "function") {
    ref(value);
  } else if (ref) {
    (ref as MutableRefObject<Value | null>).current = value;
  }
};

const restoreFocus = (
  explicitRef: DialogProps["returnFocusRef"],
  previousRef: { current: HTMLElement | null },
) => {
  const element = explicitRef?.current ?? previousRef.current;
  if (element && typeof element.focus === "function" && element.isConnected !== false) {
    element.focus();
  }
};

export const Dialog = forwardRef<HTMLDialogElement, DialogProps>(
  (
    {
      children,
      closeLabel = "Закрыть окно",
      closeOnBackdrop = false,
      closeOnEscape = true,
      closeOnOverlayClick,
      description,
      footer,
      initialFocusRef,
      isOpen,
      onCancel,
      onClick,
      onClose,
      onKeyDown,
      onOpenChange,
      open,
      returnFocusRef,
      showCloseButton = true,
      size = "medium",
      title,
      className,
      ...dialogProps
    },
    forwardedRef,
  ) => {
    const isDialogOpen = open ?? isOpen ?? false;
    const dialogRef = useRef<HTMLDialogElement | null>(null);
    const previousFocusRef = useRef<HTMLElement | null>(null);
    const isModalRef = useRef(false);
    const openRef = useRef(isDialogOpen);
    const closeRequestRef = useRef(false);
    const escapeHandledRef = useRef(false);
    const titleId = useId();
    const descriptionId = useId();

    useEffect(() => {
      openRef.current = isDialogOpen;
    }, [isDialogOpen]);

    const setDialogRef = useCallback(
      (node: HTMLDialogElement | null) => {
        dialogRef.current = node;
        assignRef(forwardedRef, node);
      },
      [forwardedRef],
    );

    const focusFirstElement = useCallback(() => {
      const dialog = dialogRef.current;
      if (!dialog) {
        return;
      }

      const target = initialFocusRef?.current;
      if (target && dialog.contains(target)) {
        target.focus();
        return;
      }

      const firstFocusable = dialog.querySelector<HTMLElement>(FOCUSABLE_SELECTOR);
      (firstFocusable ?? dialog).focus();
    }, [initialFocusRef]);

    const requestClose = useCallback(() => {
      if (!openRef.current || closeRequestRef.current) {
        return;
      }

      closeRequestRef.current = true;
      onOpenChange?.(false);
      onClose?.();
    }, [onClose, onOpenChange]);

    useEffect(() => {
      const dialog = dialogRef.current;
      if (!dialog) {
        return;
      }

      if (isDialogOpen) {
        if (!isModalRef.current) {
          const activeElement = document.activeElement;
          previousFocusRef.current = activeElement instanceof HTMLElement ? activeElement : null;

          try {
            if (typeof dialog.showModal === "function") {
              dialog.showModal();
            } else {
              dialog.setAttribute("open", "");
            }
            isModalRef.current = true;
          } catch {
            // A non-browser renderer or an already-open dialog still gets the
            // native `open` state and the same focus behaviour.
            dialog.setAttribute("open", "");
            isModalRef.current = true;
          }

          focusFirstElement();
          if (typeof window.requestAnimationFrame === "function") {
            window.requestAnimationFrame(focusFirstElement);
          } else {
            window.setTimeout(focusFirstElement, 0);
          }
        }
        return;
      }

      if (isModalRef.current || dialog.open) {
        isModalRef.current = false;
        try {
          if (dialog.open) {
            dialog.close();
          }
        } catch {
          dialog.removeAttribute("open");
        }
        restoreFocus(returnFocusRef, previousFocusRef);
      }
      closeRequestRef.current = false;
    }, [focusFirstElement, isDialogOpen, returnFocusRef]);

    useEffect(
      () => () => {
        const dialog = dialogRef.current;
        if (dialog?.open) {
          try {
            dialog.close();
          } catch {
            dialog.removeAttribute("open");
          }
        }
        restoreFocus(returnFocusRef, previousFocusRef);
      },
      [returnFocusRef],
    );

    const handleCancel = (event: SyntheticEvent<HTMLDialogElement>) => {
      if (!closeOnEscape || escapeHandledRef.current) {
        event.preventDefault();
        escapeHandledRef.current = false;
        return;
      }

      event.preventDefault();
      onCancel?.();
      requestClose();
    };

    const handleKeyDown = (event: KeyboardEvent<HTMLDialogElement>) => {
      onKeyDown?.(event);

      if (event.key === "Escape") {
        if (!closeOnEscape) {
          event.preventDefault();
          return;
        }

        event.preventDefault();
        escapeHandledRef.current = true;
        onCancel?.();
        requestClose();
        queueMicrotask(() => {
          escapeHandledRef.current = false;
        });
        return;
      }

      if (event.key !== "Tab") {
        return;
      }

      const dialog = dialogRef.current;
      if (!dialog) {
        return;
      }

      const focusable = Array.from(dialog.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)).filter(
        (element) => element.getAttribute("aria-hidden") !== "true",
      );
      if (focusable.length === 0) {
        event.preventDefault();
        dialog.focus();
        return;
      }

      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      const active = document.activeElement;
      if (event.shiftKey && active === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && active === last) {
        event.preventDefault();
        first.focus();
      }
    };

    const handleClose = () => {
      isModalRef.current = false;
      if (!closeRequestRef.current && openRef.current) {
        onOpenChange?.(false);
        onClose?.();
      }
      closeRequestRef.current = false;
      restoreFocus(returnFocusRef, previousFocusRef);
    };

    const handleClick = (event: MouseEvent<HTMLDialogElement>) => {
      onClick?.(event);
      if (
        (closeOnBackdrop || closeOnOverlayClick) &&
        event.target === event.currentTarget
      ) {
        requestClose();
      }
    };

    return (
      <dialog
        {...dialogProps}
        ref={setDialogRef}
        className={[styles.dialog, styles[`dialog-${size}`], className]
          .filter(Boolean)
          .join(" ")}
        aria-modal="true"
        aria-labelledby={title !== undefined ? titleId : dialogProps["aria-labelledby"]}
        aria-describedby={
          description !== undefined ? descriptionId : dialogProps["aria-describedby"]
        }
        tabIndex={-1}
        onCancel={handleCancel}
        onClose={handleClose}
        onClick={handleClick}
        onKeyDown={handleKeyDown}
      >
        {(title !== undefined || showCloseButton) && (
          <header className={styles.dialogHeader}>
            {title !== undefined && (
              <h2 className={styles.dialogTitle} id={titleId}>
                {title}
              </h2>
            )}
            {showCloseButton && (
              <button
                type="button"
                className={styles.dialogClose}
                aria-label={closeLabel}
                onClick={requestClose}
              >
                <span aria-hidden="true">x</span>
              </button>
            )}
          </header>
        )}
        {description !== undefined && (
          <p className={styles.dialogDescription} id={descriptionId}>
            {description}
          </p>
        )}
        <div className={styles.dialogBody}>{children}</div>
        {footer !== undefined && <footer className={styles.dialogFooter}>{footer}</footer>}
      </dialog>
    );
  },
);

Dialog.displayName = "Dialog";
