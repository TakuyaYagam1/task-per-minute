import {
  useId,
  type HTMLAttributes,
  type ReactNode,
} from "react";

import styles from "./TournamentUi.module.css";

export type PanelTone = "default" | "muted" | "accent" | "success" | "error";

export interface PanelProps extends Omit<HTMLAttributes<HTMLElement>, "title"> {
  children?: ReactNode;
  /** The semantic element used for the panel root. */
  as?: "article" | "div" | "section";
  title?: ReactNode;
  description?: ReactNode;
  header?: ReactNode;
  footer?: ReactNode;
  tone?: PanelTone;
  interactive?: boolean;
}

export const Panel = ({
  as = "section",
  children,
  className,
  description,
  footer,
  header,
  interactive = false,
  title,
  tone = "default",
  ...panelProps
}: PanelProps) => {
  const titleId = useId();
  const Component = as;
  const hasCustomHeader = header !== undefined && header !== null;
  const hasHeading =
    title !== undefined || description !== undefined || hasCustomHeader;

  return (
    <Component
      {...panelProps}
      className={[styles.panel, styles[`panel-${tone}`], className]
        .filter(Boolean)
        .join(" ")}
      data-interactive={interactive ? "true" : undefined}
      tabIndex={interactive ? (panelProps.tabIndex ?? 0) : panelProps.tabIndex}
      aria-labelledby={
        panelProps["aria-labelledby"] ??
        (title !== undefined && !hasCustomHeader ? titleId : undefined)
      }
    >
      {hasHeading && (
        <div className={styles.panelHeader}>
          {header ?? (
            <div>
              {title !== undefined && (
                <h2 className={styles.panelTitle} id={titleId}>
                  {title}
                </h2>
              )}
              {description !== undefined && (
                <p className={styles.panelDescription}>{description}</p>
              )}
            </div>
          )}
        </div>
      )}
      <div className={styles.panelBody}>{children}</div>
      {footer !== undefined && <footer className={styles.panelFooter}>{footer}</footer>}
    </Component>
  );
};

Panel.displayName = "Panel";
