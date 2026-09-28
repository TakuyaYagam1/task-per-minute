"use client";

import { useEffect, useId, useMemo, useRef, useState } from "react";

import type { AdminPlayer } from "../../shared/api";

import styles from "./PlayerPicker.module.css";

type PlayerOption = Pick<AdminPlayer, "id" | "username">;

type PlayerPickerProps = Readonly<{
  id: string;
  players: readonly PlayerOption[];
  value: string;
  onChange: (playerId: string) => void;
  onOpen?: () => void;
  selectedPlayer?: PlayerOption | null;
  excludedPlayerIds?: ReadonlySet<string>;
  disabled?: boolean;
  describedBy?: string;
}>;

const MAX_VISIBLE_RESULTS = 20;

const PlayerPicker = ({
  describedBy,
  disabled = false,
  excludedPlayerIds,
  id,
  onChange,
  onOpen,
  players,
  selectedPlayer,
  value,
}: PlayerPickerProps) => {
  const listboxId = useId();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const resultListRef = useRef<HTMLUListElement | null>(null);
  const activeOptionRef = useRef<HTMLLIElement | null>(null);

  const availablePlayers = useMemo(() => {
    const seen = new Set<string>();
    return players.filter((player) => {
      if (seen.has(player.id) || excludedPlayerIds?.has(player.id)) {
        return false;
      }
      seen.add(player.id);
      return true;
    });
  }, [excludedPlayerIds, players]);

  const normalizedQuery = query.trim().toLocaleLowerCase();
  const matchingPlayers = useMemo(
    () =>
      availablePlayers.filter((player) =>
        normalizedQuery
          ? player.username.toLocaleLowerCase().includes(normalizedQuery)
          : true,
      ),
    [availablePlayers, normalizedQuery],
  );
  const visiblePlayers = matchingPlayers.slice(0, MAX_VISIBLE_RESULTS);
  const selected =
    selectedPlayer && selectedPlayer.id === value ? selectedPlayer : null;
  const selectedLabel = selected?.username || "";
  const activePlayer = visiblePlayers[activeIndex];
  const expanded = open && !disabled;

  useEffect(() => {
    setActiveIndex((current) =>
      visiblePlayers.length === 0
        ? 0
        : Math.min(current, visiblePlayers.length - 1),
    );
  }, [visiblePlayers.length]);

  useEffect(() => {
    if (!open || !resultListRef.current || !activeOptionRef.current) {
      return;
    }
    const listRect = resultListRef.current.getBoundingClientRect();
    const optionRect = activeOptionRef.current.getBoundingClientRect();
    if (optionRect.top < listRect.top) {
      resultListRef.current.scrollTop -= listRect.top - optionRect.top;
    } else if (optionRect.bottom > listRect.bottom) {
      resultListRef.current.scrollTop += optionRect.bottom - listRect.bottom;
    }
  }, [activeIndex, activePlayer?.id, normalizedQuery, open]);

  const openPicker = (): void => {
    if (disabled) {
      return;
    }
    if (!open) {
      setOpen(true);
      setQuery("");
      setActiveIndex(0);
      onOpen?.();
    }
  };

  const selectPlayer = (playerId: string): void => {
    if (disabled) {
      return;
    }
    onChange(playerId);
    setQuery("");
    setOpen(false);
    setActiveIndex(0);
  };

  const handleKeyDown = (event: React.KeyboardEvent<HTMLInputElement>): void => {
    if (event.key === "Escape") {
      if (open) {
        event.preventDefault();
        setOpen(false);
        setQuery("");
      }
      return;
    }

    if (event.key === "ArrowDown") {
      event.preventDefault();
      if (!open) {
        openPicker();
        return;
      }
      if (visiblePlayers.length > 0) {
        setActiveIndex((current) => (current + 1) % visiblePlayers.length);
      }
      return;
    }

    if (event.key === "ArrowUp") {
      event.preventDefault();
      if (!open) {
        openPicker();
        return;
      }
      if (visiblePlayers.length > 0) {
        setActiveIndex(
          (current) =>
            (current - 1 + visiblePlayers.length) % visiblePlayers.length,
        );
      }
      return;
    }

    if (event.key === "Enter" && !open) {
      event.preventDefault();
      openPicker();
      return;
    }

    if (event.key === "Enter" && open) {
      event.preventDefault();
      if (activePlayer) {
        selectPlayer(activePlayer.id);
      }
    }
  };

  return (
    <div
      className={styles.picker}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
          setOpen(false);
          setQuery("");
        }
      }}
    >
      <input
        id={id}
        className={styles.input}
        type="text"
        role="combobox"
        value={expanded ? query : selectedLabel}
        placeholder="Поиск по никнейму"
        autoComplete="off"
        aria-autocomplete="list"
        aria-controls={listboxId}
        aria-expanded={expanded}
        aria-activedescendant={
          expanded && activePlayer ? `${listboxId}-${activePlayer.id}` : undefined
        }
        aria-describedby={describedBy}
        disabled={disabled}
        onFocus={openPicker}
        onClick={openPicker}
        onChange={(event) => {
          if (!open) {
            openPicker();
          }
          setQuery(event.target.value);
          setActiveIndex(0);
        }}
        onKeyDown={handleKeyDown}
      />

      {expanded && selected ? (
        <p className={styles.selected} aria-live="polite">
          Текущий выбор: <strong>{selected.username}</strong>
        </p>
      ) : null}

      {expanded && (
        <div className={styles.results}>
          <ul
            ref={resultListRef}
            id={listboxId}
            className={styles.resultList}
            role="listbox"
            aria-label="Результаты поиска игроков"
          >
            {visiblePlayers.map((player, index) => (
              <li
                key={player.id}
                id={`${listboxId}-${player.id}`}
                className={index === activeIndex ? styles.activeResult : styles.result}
                ref={index === activeIndex ? activeOptionRef : undefined}
                role="option"
                aria-selected={player.id === value}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => selectPlayer(player.id)}
              >
                <span className={styles.playerName}>{player.username}</span>
              </li>
            ))}
          </ul>

          {matchingPlayers.length === 0 && (
            <p className={styles.resultHint}>Игроки по этому никнейму не найдены.</p>
          )}
          {matchingPlayers.length > MAX_VISIBLE_RESULTS && (
            <p className={styles.resultHint}>
              Показаны первые {MAX_VISIBLE_RESULTS} из {matchingPlayers.length}. Уточните поиск.
            </p>
          )}
          {value && (
            <button
              className={styles.clearAction}
              type="button"
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => selectPlayer("")}
            >
              Очистить выбор
            </button>
          )}
        </div>
      )}
    </div>
  );
};

PlayerPicker.displayName = "PlayerPicker";

export { PlayerPicker };
