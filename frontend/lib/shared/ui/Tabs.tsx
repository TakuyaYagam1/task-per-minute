"use client";

import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type HTMLAttributes,
  type KeyboardEvent,
  type ReactNode,
} from "react";

import styles from "./TournamentUi.module.css";

export type TabsOrientation = "horizontal" | "vertical";
export type TabsValue = string | number;

export interface TabItem {
  /** Stable identifier. `value` is accepted as an ergonomic alias. */
  id?: string;
  value?: string | number;
  label: ReactNode;
  panel?: ReactNode;
  disabled?: boolean;
}

export interface TabsProps extends Omit<HTMLAttributes<HTMLDivElement>, "children" | "onChange"> {
  children?: ReactNode;
  items?: readonly TabItem[];
  /** `tabs` is an alias for `items` for existing call sites. */
  tabs?: readonly TabItem[];
  value?: TabsValue;
  activeTab?: TabsValue;
  defaultValue?: TabsValue;
  defaultTab?: TabsValue;
  onChange?: (value: string) => void;
  onTabChange?: (value: string) => void;
  ariaLabel?: string;
  orientation?: TabsOrientation;
  renderPanel?: (item: TabItem, value: string) => ReactNode;
}

const normalizeTabId = (item: TabItem, index: number) =>
  String(item.id ?? item.value ?? `tab-${index}`);

const firstEnabledId = (items: readonly TabItem[]) => {
  const index = items.findIndex((item) => !item.disabled);
  return index === -1 ? null : normalizeTabId(items[index], index);
};

const safeIdPart = (value: string) => value.replace(/[^a-zA-Z0-9_-]/g, "-");

export const Tabs = ({
  activeTab,
  "aria-label": ariaLabelProp,
  ariaLabel,
  children,
  className,
  defaultTab,
  defaultValue,
  items,
  onChange,
  onTabChange,
  orientation = "horizontal",
  renderPanel,
  tabs,
  value,
  ...tabProps
}: TabsProps) => {
  const tabItems = useMemo(() => items ?? tabs ?? [], [items, tabs]);
  const rootId = useId().replace(/:/g, "");
  const normalizedItems = useMemo(
    () => tabItems.map((item, index) => ({ item, id: normalizeTabId(item, index), index })),
    [tabItems],
  );
  const requestedValue =
    value === undefined && activeTab === undefined
      ? undefined
      : String(value ?? activeTab);
  const requestedDefault =
    defaultValue === undefined && defaultTab === undefined
      ? undefined
      : String(defaultValue ?? defaultTab);
  const initialValue =
    requestedValue ??
    (requestedDefault && normalizedItems.some(({ id }) => id === requestedDefault)
      ? requestedDefault
      : firstEnabledId(tabItems));
  const [uncontrolledValue, setUncontrolledValue] = useState<string | null>(initialValue);
  const selectedValue = requestedValue ?? uncontrolledValue;
  const selectedEntry =
    normalizedItems.find(({ id, item }) => id === selectedValue && !item.disabled) ??
    normalizedItems.find(({ item }) => !item.disabled);
  const resolvedSelectedValue = selectedEntry?.id ?? null;
  const [focusValue, setFocusValue] = useState<string | null>(resolvedSelectedValue);
  const tabRefs = useRef(new Map<string, HTMLButtonElement>());

  useEffect(() => {
    const available = new Set(normalizedItems.filter(({ item }) => !item.disabled).map(({ id }) => id));
    const fallback = firstEnabledId(tabItems);

    setFocusValue((current) => (current && available.has(current) ? current : fallback));
    if (requestedValue === undefined) {
      setUncontrolledValue((current) => (current && available.has(current) ? current : fallback));
    }
  }, [normalizedItems, requestedValue, tabItems]);

  const selectValue = useCallback(
    (nextValue: string) => {
      const nextEntry = normalizedItems.find(({ id }) => id === nextValue);
      if (!nextEntry || nextEntry.item.disabled) {
        return;
      }

      setFocusValue(nextValue);
      if (requestedValue === undefined) {
        setUncontrolledValue(nextValue);
      }
      onChange?.(nextValue);
      onTabChange?.(nextValue);
    },
    [normalizedItems, onChange, onTabChange, requestedValue],
  );

  const focusTab = useCallback((nextValue: string) => {
    setFocusValue(nextValue);
    const tab = tabRefs.current.get(nextValue);
    if (tab) {
      tab.focus();
    }
  }, []);

  const handleKeyDown = (event: KeyboardEvent<HTMLButtonElement>, currentValue: string) => {
    const enabled = normalizedItems.filter(({ item }) => !item.disabled);
    const currentIndex = enabled.findIndex(({ id }) => id === currentValue);
    if (currentIndex === -1) {
      return;
    }

    const previousKey = orientation === "vertical" ? "ArrowUp" : "ArrowLeft";
    const nextKey = orientation === "vertical" ? "ArrowDown" : "ArrowRight";
    let nextIndex: number | null = null;

    if (
      event.key === previousKey ||
      (orientation === "vertical" && event.key === "ArrowLeft")
    ) {
      nextIndex = (currentIndex - 1 + enabled.length) % enabled.length;
    } else if (
      event.key === nextKey ||
      (orientation === "vertical" && event.key === "ArrowRight")
    ) {
      nextIndex = (currentIndex + 1) % enabled.length;
    } else if (event.key === "Home") {
      nextIndex = 0;
    } else if (event.key === "End") {
      nextIndex = enabled.length - 1;
    }

    if (nextIndex === null) {
      return;
    }

    event.preventDefault();
    const nextValue = enabled[nextIndex].id;
    selectValue(nextValue);
    focusTab(nextValue);
  };

  const tabListLabel = ariaLabel ?? ariaLabelProp ?? "Разделы турнира";

  return (
    <div
      {...tabProps}
      className={[styles.tabs, orientation === "vertical" && styles.tabsVertical, className]
        .filter(Boolean)
        .join(" ")}
      data-orientation={orientation}
    >
      <div
        className={styles.tabList}
        role="tablist"
        aria-label={tabListLabel}
        aria-orientation={orientation}
      >
        {normalizedItems.map(({ item, id }) => {
          const tabId = `${rootId}-tab-${safeIdPart(id)}`;
          const panelId = `${rootId}-panel-${safeIdPart(id)}`;
          const selected = id === resolvedSelectedValue;
          const focused = id === focusValue;

          return (
            <button
              key={id}
              ref={(node) => {
                if (node) {
                  tabRefs.current.set(id, node);
                } else {
                  tabRefs.current.delete(id);
                }
              }}
              type="button"
              className={styles.tab}
              role="tab"
              id={tabId}
              aria-selected={selected}
              aria-controls={panelId}
              aria-disabled={item.disabled || undefined}
              disabled={item.disabled}
              tabIndex={focused ? 0 : -1}
              onClick={() => selectValue(id)}
              onFocus={() => setFocusValue(id)}
              onKeyDown={(event) => handleKeyDown(event, id)}
            >
              {item.label}
            </button>
          );
        })}
      </div>
      {resolvedSelectedValue && (
        <div
          className={styles.tabPanel}
          role="tabpanel"
          id={`${rootId}-panel-${safeIdPart(resolvedSelectedValue)}`}
          aria-labelledby={`${rootId}-tab-${safeIdPart(resolvedSelectedValue)}`}
          tabIndex={0}
        >
          {renderPanel
            ? renderPanel(selectedEntry!.item, resolvedSelectedValue)
            : selectedEntry?.item.panel ?? children}
        </div>
      )}
    </div>
  );
};

Tabs.displayName = "Tabs";
