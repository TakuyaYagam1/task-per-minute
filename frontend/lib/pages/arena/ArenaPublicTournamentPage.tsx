"use client";

import Link from "next/link";
import {
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";

import {
  catalogCreatedLabel,
  catalogFormatLabel,
  catalogGroupLabel,
  catalogRosterLabel,
  catalogScheduleLabel,
  catalogStageLabel,
  catalogStateLabel,
  toPublicTournamentCatalogItemView,
  type PublicTournamentCatalogItemView,
} from "../../entities/tournament";
import {
  ApiError,
  getPublicTournamentByPublicId,
  openPublicTournamentEvents,
} from "../../shared/api";
import {
  buildArenaPublicTournamentPath,
  getSafeArenaPublicReturnPath,
  isArenaPublicView,
  isSafePublicTournamentId,
  type ArenaPublicView,
} from "../../shared/lib";
import { Button, Dialog, Message, Status } from "../../shared/ui";
import {
  TournamentRecoveryPanel,
  type TournamentRecoveryRenderContext,
} from "../../features/tournament-live";
import { TournamentEntry } from "../../features/tournament-entry";
import { TestBotsPanel } from "../../widgets/test-bots";
import {
  TournamentBroadcastPanel,
  type TournamentBroadcastView,
} from "../../widgets/tournament-broadcast";
import { ArenaShell } from "../../widgets/arena";
import styles from "./ArenaPublicTournamentPage.module.css";

type PublicTournamentLoadState = Readonly<{
  status: "loading" | "ready" | "not_found" | "rate_limited" | "error";
  item: PublicTournamentCatalogItemView | null;
  error: string | null;
  metadataStale: boolean;
  deletionConfirmed: boolean;
}>;

const initialLoadState: PublicTournamentLoadState = {
  status: "loading",
  item: null,
  error: null,
  metadataStale: false,
  deletionConfirmed: false,
};

const unavailableTournamentMessage =
  "Соревнование не найдено или больше не публикуется.";
const deletedTournamentMessage =
  "Соревнование удалено или снято с публикации. Вернитесь к списку соревнований.";
const registrationMetadataRefreshIntervalMs = 5_000;
const metadataRefreshIntervalMs = 15_000;

type ArenaPublicTournamentPageProps = Readonly<{
  publicId: string;
}>;

type PublicLocationState = Readonly<{
  match: string | null;
  returnPath: string | null;
}>;

const initialLocationState: PublicLocationState = {
  match: null,
  returnPath: null,
};

const viewItems: readonly Readonly<{
  view: ArenaPublicView;
  label: string;
}>[] = [
  { view: "overview", label: "Обзор" },
  { view: "matches", label: "Матчи" },
  { view: "bracket", label: "Сетка" },
  { view: "standings", label: "Таблица соревнования" },
];

const viewFromLocation = (): ArenaPublicView => {
  if (typeof window === "undefined") {
    return "overview";
  }
  const value = new URLSearchParams(window.location.search).get("view");
  return isArenaPublicView(value) ? value : "overview";
};

const locationStateFromWindow = (): PublicLocationState => {
  if (typeof window === "undefined") {
    return initialLocationState;
  }
  const search = new URLSearchParams(window.location.search);
  return {
    match: search.get("match"),
    returnPath: getSafeArenaPublicReturnPath(search.get("return")),
  };
};

const getSafeArenaCatalogReturnPath = (value: string | null): string => {
  const safePath = getSafeArenaPublicReturnPath(value);
  if (safePath && (safePath === "/arena" || safePath.startsWith("/arena?"))) {
    return safePath;
  }
  return "/arena";
};

const errorFor = (error: unknown): Pick<
  PublicTournamentLoadState,
  "status" | "error" | "deletionConfirmed"
> => {
  if (error instanceof ApiError) {
    if (error.kind === "not_found") {
      return {
        status: "not_found",
        error: unavailableTournamentMessage,
        deletionConfirmed: false,
      };
    }
    if (error.kind === "rate_limited") {
      return {
        status: "rate_limited",
        error: "Слишком много запросов. Повторите попытку позже.",
        deletionConfirmed: false,
      };
    }
  }
  return {
    status: "error",
    error: "Не удалось загрузить соревнование. Повторите попытку.",
    deletionConfirmed: false,
  };
};

const isAbortError = (error: unknown): boolean =>
  error instanceof DOMException &&
  error.name === "AbortError";

const broadcastViewFor = (view: ArenaPublicView): TournamentBroadcastView =>
  view === "overview" ? "overview" : view;

const isPrestartItem = (item: PublicTournamentCatalogItemView): boolean =>
  item.group === "upcoming";

type PublicBroadcastProps = Readonly<{
  item: PublicTournamentCatalogItemView;
  onRosterSizeChange: (tournamentId: string, rosterSize: number) => void;
  onTournamentMissing: (tournamentId: string) => void;
  view: ArenaPublicView;
}>;

const PublicBroadcast = ({ item, onRosterSizeChange, onTournamentMissing, view }: PublicBroadcastProps) => (
  <TournamentRecoveryPanel contentOnly role="spectator" tournamentId={item.tournamentId}>
    {(context) => (
      <PublicBroadcastContent
        context={context}
        onRosterSizeChange={onRosterSizeChange}
        onTournamentMissing={onTournamentMissing}
        tournamentId={item.tournamentId}
        view={view}
      />
    )}
  </TournamentRecoveryPanel>
);

const PublicBroadcastContent = ({
  context,
  onRosterSizeChange,
  onTournamentMissing,
  tournamentId,
  view,
}: Readonly<{
  context: TournamentRecoveryRenderContext;
  onRosterSizeChange: (tournamentId: string, rosterSize: number) => void;
  onTournamentMissing: (tournamentId: string) => void;
  tournamentId: string;
  view: ArenaPublicView;
}>) => {
  const rosterSize = context.publicState?.display.tournament.roster_size;
  const tournamentMissing = context.recoveryError?.kind === "not_found";

  useEffect(() => {
    if (typeof rosterSize === "number" && Number.isSafeInteger(rosterSize) && rosterSize >= 0) {
      onRosterSizeChange(tournamentId, rosterSize);
    }
  }, [onRosterSizeChange, rosterSize, tournamentId]);

  useEffect(() => {
    if (tournamentMissing) {
      onTournamentMissing(tournamentId);
    }
  }, [onTournamentMissing, tournamentId, tournamentMissing]);

  const noPublicState = context.recovery === null && context.publicState === null;
  if (noPublicState) {
    if (context.recoveryError?.kind === "not_found") {
      return (
        <Message tone="info" title="Матчи еще не опубликованы">
          <p>Матчи и результаты появятся после старта соревнования.</p>
          <Button type="button" size="small" variant="secondary" onClick={context.retry}>
            Проверить снова
          </Button>
        </Message>
      );
    }
    if (context.recoveryError) {
      return (
        <Message
          tone={context.recoveryError.kind === "rate_limited" || context.recoveryError.kind === "transport"
            ? "warning"
            : "error"}
          title="Не удалось загрузить матчи"
        >
          <p>Матчи и таблица временно недоступны. Повторите попытку.</p>
          <Button type="button" size="small" variant="secondary" onClick={context.retry}>
            Повторить
          </Button>
        </Message>
      );
    }
    return <Status tone="loading">Загрузка матчей</Status>;
  }

  return (
    <>
      {context.recoveryError && (
        <Message
          tone={context.recoveryError.kind === "rate_limited" || context.recoveryError.kind === "transport"
            ? "warning"
            : "error"}
          title="Данные могли устареть"
        >
          <p>Показываем последние подтвержденные данные.</p>
          <Button type="button" size="small" variant="secondary" onClick={context.retry}>
            Повторить
          </Button>
        </Message>
      )}
      <TournamentBroadcastPanel
        connectionStatus={context.publicConnectionStatus}
        receivedAtMonotonicMs={context.receivedAtMonotonicMs}
        serverTimestamp={context.recovery?.serverTimestamp}
        state={context.publicState}
        view={broadcastViewFor(view)}
      />
    </>
  );
};

export const ArenaPublicTournamentPage = ({
  publicId,
}: ArenaPublicTournamentPageProps) => {
  const [view, setView] = useState<ArenaPublicView>("overview");
  const [locationState, setLocationState] = useState<PublicLocationState>(initialLocationState);
  const [loadState, setLoadState] = useState<PublicTournamentLoadState>(initialLoadState);
  const [liveRosterSize, setLiveRosterSize] = useState<Readonly<{
    tournamentId: string;
    rosterSize: number;
    updatedAtMs: number;
  }> | null>(null);
  const [admissionRefreshVersion, setAdmissionRefreshVersion] = useState(0);
  const [reloadVersion, setReloadVersion] = useState(0);
  const requestRef = useRef(0);
  const metadataRequestRef = useRef(0);
  const refreshMetadataRef = useRef<() => void>(() => {});
  const missingCheckRef = useRef<Readonly<{
    controller: AbortController;
    publicId: string;
    tournamentId: string;
  }> | null>(null);
  const shouldPollMetadata =
    loadState.status === "ready" && loadState.item?.publicId === publicId;
  const currentMetadataRefreshIntervalMs = loadState.item?.state === "registration"
    ? registrationMetadataRefreshIntervalMs
    : metadataRefreshIntervalMs;

  const handleRosterSizeChange = useCallback((tournamentId: string, rosterSize: number): void => {
    setLiveRosterSize({ tournamentId, rosterSize, updatedAtMs: Date.now() });
  }, []);

  const handleTournamentMissing = useCallback((tournamentId: string): void => {
    const currentPublicId = loadState.item?.publicId;
    const currentTournamentId = loadState.item?.tournamentId;
    if (currentPublicId === undefined || currentTournamentId !== tournamentId) {
      return;
    }
    const existingCheck = missingCheckRef.current;
    if (
      existingCheck?.publicId === currentPublicId &&
      existingCheck.tournamentId === tournamentId
    ) {
      return;
    }

    existingCheck?.controller.abort();
    const controller = new AbortController();
    missingCheckRef.current = { controller, publicId: currentPublicId, tournamentId };
    void getPublicTournamentByPublicId(currentPublicId, controller.signal)
      .then((response) => {
        const nextItem = toPublicTournamentCatalogItemView(response);
        if (
          controller.signal.aborted ||
          nextItem.publicId !== currentPublicId ||
          nextItem.tournamentId !== tournamentId
        ) {
          return;
        }
        setLoadState((current) => (
          current.item?.publicId === currentPublicId && current.item.tournamentId === tournamentId
            ? { ...current, item: nextItem, metadataStale: false }
            : current
        ));
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted || isAbortError(error)) {
          return;
        }
        if (error instanceof ApiError && error.kind === "not_found") {
          setLoadState((current) => (
            current.item?.publicId === currentPublicId && current.item.tournamentId === tournamentId
              ? {
                status: "not_found",
                item: null,
                error: deletedTournamentMessage,
                metadataStale: false,
                deletionConfirmed: true,
              }
              : current
          ));
          return;
        }
        setLoadState((current) => (
          current.item?.publicId === currentPublicId && current.item.tournamentId === tournamentId
            ? { ...current, metadataStale: true }
            : current
        ));
      })
      .finally(() => {
        if (missingCheckRef.current?.controller === controller) {
          missingCheckRef.current = null;
        }
      });
  }, [loadState.item?.publicId, loadState.item?.tournamentId]);

  const handleEntryRosterSizeChange = useCallback((rosterSize: number): void => {
    const tournamentId = loadState.item?.tournamentId;
    if (tournamentId !== undefined) {
      handleRosterSizeChange(tournamentId, rosterSize);
    }
  }, [handleRosterSizeChange, loadState.item?.tournamentId]);

  useEffect(() => {
    const syncLocation = (): void => {
      setView(viewFromLocation());
      setLocationState(locationStateFromWindow());
    };
    syncLocation();
    const handlePopState = (): void => {
      syncLocation();
    };
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, [publicId]);

  useEffect(() => () => {
    missingCheckRef.current?.controller.abort();
  }, [publicId]);

  useEffect(() => {
    const controller = new AbortController();
    const requestId = ++requestRef.current;
    if (!isSafePublicTournamentId(publicId)) {
      setLoadState({
        status: "not_found",
        item: null,
        error: unavailableTournamentMessage,
        metadataStale: false,
        deletionConfirmed: false,
      });
      return () => controller.abort();
    }

    setLoadState((current) => ({
      status: "loading",
      item: current.item?.publicId === publicId ? current.item : null,
      error: null,
      metadataStale: false,
      deletionConfirmed: false,
    }));
    void getPublicTournamentByPublicId(publicId, controller.signal)
      .then((item) => {
        const viewItem = toPublicTournamentCatalogItemView(item);
        if (
          controller.signal.aborted ||
          requestId !== requestRef.current ||
          viewItem.publicId !== publicId
        ) {
          return;
        }
        setLoadState({
          status: "ready",
          item: viewItem,
          error: null,
          metadataStale: false,
          deletionConfirmed: false,
        });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted || requestId !== requestRef.current || isAbortError(error)) {
          return;
        }
        setLoadState({
          ...errorFor(error),
          item: null,
          metadataStale: false,
        });
      });

    return () => {
      controller.abort();
    };
  }, [publicId, reloadVersion]);

  useEffect(() => {
    if (!shouldPollMetadata || typeof document === "undefined") {
      return undefined;
    }
    const controller = new AbortController();
    let active = true;
    let inFlight = false;
    let pendingRefresh = false;

    const refreshMetadata = (): void => {
      if (!active || document.visibilityState !== "visible") {
        return;
      }
      if (inFlight) {
        pendingRefresh = true;
        return;
      }
      inFlight = true;
      const requestId = ++metadataRequestRef.current;
      const requestStartedAt = Date.now();
      void getPublicTournamentByPublicId(publicId, controller.signal)
        .then((response) => {
          const nextItem = toPublicTournamentCatalogItemView(response);
          if (
            !active ||
            controller.signal.aborted ||
            requestId !== metadataRequestRef.current
          ) {
            return;
          }
          setLiveRosterSize((current) => (
            current?.tournamentId === nextItem.tournamentId &&
            current.updatedAtMs > requestStartedAt
              ? current
              : null
          ));
          setLoadState((current) => {
            if (
              current.item?.publicId !== publicId ||
              current.item.tournamentId !== nextItem.tournamentId ||
              nextItem.publicId !== publicId
            ) {
              return { ...current, metadataStale: true };
            }
            return { ...current, item: nextItem, metadataStale: false };
          });
        })
        .catch((error: unknown) => {
          if (
            !active ||
            controller.signal.aborted ||
            requestId !== metadataRequestRef.current ||
            isAbortError(error)
          ) {
            return;
          }
          if (error instanceof ApiError && error.kind === "not_found") {
            setLoadState((current) => (
              current.item?.publicId === publicId
                ? {
                  status: "not_found",
                  item: null,
                  error: deletedTournamentMessage,
                  metadataStale: false,
                  deletionConfirmed: true,
                }
                : current
            ));
            return;
          }
          setLoadState((current) => (
            current.item?.publicId === publicId
              ? { ...current, metadataStale: true }
              : current
          ));
        })
        .finally(() => {
          inFlight = false;
          if (active && pendingRefresh) {
            pendingRefresh = false;
            refreshMetadata();
          }
        });
    };

    refreshMetadataRef.current = refreshMetadata;

    const interval = window.setInterval(refreshMetadata, currentMetadataRefreshIntervalMs);
    const handleVisibilityChange = (): void => {
      if (document.visibilityState === "visible") {
        refreshMetadata();
      }
    };
    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      active = false;
      controller.abort();
      if (refreshMetadataRef.current === refreshMetadata) {
        refreshMetadataRef.current = () => {};
      }
      window.clearInterval(interval);
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [currentMetadataRefreshIntervalMs, publicId, reloadVersion, shouldPollMetadata]);

  useEffect(() => {
    if (!shouldPollMetadata || typeof document === "undefined") {
      return undefined;
    }

    let active = true;
    let source: EventSource | null = null;
    let retryTimer: number | undefined;
    let invalidationTimer: number | undefined;
    let retryAttempt = 0;

    const closeSource = (): void => {
      if (source === null) {
        return;
      }
      source.onerror = null;
      source.close();
      source = null;
    };

    const connect = (): void => {
      if (!active || source !== null || document.visibilityState !== "visible") {
        return;
      }
      try {
        const nextSource = openPublicTournamentEvents();
        source = nextSource;
        nextSource.addEventListener("ready", () => {
          const reconnected = retryAttempt > 0;
          retryAttempt = 0;
          if (reconnected) {
            refreshMetadataRef.current();
            setAdmissionRefreshVersion((current) => current + 1);
          }
        });
        nextSource.addEventListener("changed", (event: Event) => {
          let payload: unknown;
          try {
            payload = JSON.parse((event as MessageEvent<string>).data) as unknown;
          } catch {
            return;
          }
          if (
            typeof payload !== "object" ||
            payload === null ||
            !("topic" in payload) ||
            payload.topic !== "tournaments" ||
            invalidationTimer !== undefined
          ) {
            return;
          }
          invalidationTimer = window.setTimeout(() => {
            invalidationTimer = undefined;
            if (!active || document.visibilityState !== "visible") {
              return;
            }
            refreshMetadataRef.current();
            setAdmissionRefreshVersion((current) => current + 1);
          }, 150);
        });
        nextSource.onerror = () => {
          closeSource();
          if (!active || document.visibilityState !== "visible" || retryTimer !== undefined) {
            return;
          }
          const retryDelay = Math.min(30_000, 1_000 * 2 ** Math.min(retryAttempt, 5));
          retryAttempt += 1;
          retryTimer = window.setTimeout(() => {
            retryTimer = undefined;
            connect();
          }, retryDelay);
        };
      } catch {
        if (!active || document.visibilityState !== "visible" || retryTimer !== undefined) {
          return;
        }
        const retryDelay = Math.min(30_000, 1_000 * 2 ** Math.min(retryAttempt, 5));
        retryAttempt += 1;
        retryTimer = window.setTimeout(() => {
          retryTimer = undefined;
          connect();
        }, retryDelay);
      }
    };

    const handleVisibilityChange = (): void => {
      if (document.visibilityState !== "visible") {
        if (retryTimer !== undefined) {
          window.clearTimeout(retryTimer);
          retryTimer = undefined;
        }
        closeSource();
        return;
      }
      if (retryTimer !== undefined) {
        window.clearTimeout(retryTimer);
        retryTimer = undefined;
      }
      setAdmissionRefreshVersion((current) => current + 1);
      connect();
    };

    connect();
    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      active = false;
      if (retryTimer !== undefined) {
        window.clearTimeout(retryTimer);
      }
      if (invalidationTimer !== undefined) {
        window.clearTimeout(invalidationTimer);
      }
      closeSource();
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [publicId, shouldPollMetadata]);

  const safeReturnPath = locationState.returnPath;

  const buildViewHref = useCallback((nextView: ArenaPublicView): string => {
    const base = buildArenaPublicTournamentPath(publicId, nextView, safeReturnPath);
    if (typeof window === "undefined") {
      return base;
    }
    const url = new URL(base, window.location.origin);
    if (locationState.match) {
      url.searchParams.set("match", locationState.match);
    }
    return `${url.pathname}?${url.searchParams.toString()}`;
  }, [locationState.match, publicId, safeReturnPath]);

  const selectView = (nextView: ArenaPublicView): void => {
    const href = buildViewHref(nextView);
    window.history.pushState(window.history.state, "", href);
    setView(nextView);
  };

  const returnHref = getSafeArenaCatalogReturnPath(safeReturnPath);
  const accessStatus = loadState.status === "loading" ? "loading" :
    loadState.status === "ready" ? "ready" :
      loadState.status === "not_found" ? "missing" : "transport";

  return (
    <ArenaShell accessStatus={accessStatus}>
      <div className={styles.page}>
        <Link className={styles.backLink} href={returnHref}>
          К списку соревнований
        </Link>

        {loadState.status === "loading" && loadState.item === null && (
          <Message tone="loading" title="Загрузка соревнования">
            <p>Открываем страницу соревнования.</p>
          </Message>
        )}

        {loadState.error && (
          loadState.status !== "not_found" && (
            <Message
              tone={loadState.status === "rate_limited" ? "warning" : "error"}
              title="Соревнование недоступно"
            >
              <p>{loadState.error}</p>
              <Button
                type="button"
                size="small"
                variant="secondary"
                onClick={() => {
                  setReloadVersion((current) => current + 1);
                }}
              >
                Повторить
              </Button>
            </Message>
          )
        )}

        {loadState.status === "not_found" && !loadState.deletionConfirmed && (
          <Message tone="warning" title="Соревнование не найдено">
            <p>{loadState.error}</p>
            <Link className={styles.backLink} href={returnHref}>
              Вернуться к списку соревнований
            </Link>
          </Message>
        )}

        {loadState.status === "not_found" && loadState.deletionConfirmed && (
          <Dialog
            closeOnEscape={false}
            closeOnBackdrop={false}
            description={loadState.error ?? deletedTournamentMessage}
            footer={(
              <Link className={styles.backLink} href={returnHref}>
                Вернуться к списку соревнований
              </Link>
            )}
            open
            showCloseButton={false}
            size="small"
            title="Соревнование удалено"
          >
            <p>Откройте список соревнований, чтобы выбрать другое.</p>
          </Dialog>
        )}

        {loadState.item && (
          <>
            <header className={styles.header}>
              <div className={styles.heading}>
                <p className={styles.eyebrow}>Трансляция соревнования</p>
                <h1 className={styles.title}>{loadState.item.name}</h1>
              </div>
              <Status tone={loadState.item.group === "live" ? "live" : "info"}>
                {catalogGroupLabel(loadState.item.group)}
              </Status>
              {loadState.metadataStale && (
                <Status tone="warning">Сведения могли устареть</Status>
              )}
            </header>

            <section className={styles.metadata} aria-label="Сведения о соревновании">
              <dl className={styles.facts}>
                <div>
                  <dt>Состояние</dt>
                  <dd>{catalogStateLabel(loadState.item.state)}</dd>
                </div>
                <div>
                  <dt>Этап</dt>
                  <dd>{catalogStageLabel(loadState.item.stage)}</dd>
                </div>
                <div>
                  <dt>Участники</dt>
                  <dd>{catalogRosterLabel({
                    ...loadState.item,
                    rosterSize: liveRosterSize?.tournamentId === loadState.item.tournamentId
                      ? liveRosterSize.rosterSize
                      : loadState.item.rosterSize,
                  })}</dd>
                </div>
                <div>
                  <dt>Формат</dt>
                  <dd>{catalogFormatLabel(loadState.item.preset)}</dd>
                </div>
                <div>
                  <dt>Дата</dt>
                  <dd>{catalogScheduleLabel(loadState.item)}</dd>
                </div>
                <div>
                  <dt>Создание</dt>
                  <dd>{catalogCreatedLabel(loadState.item.createdAt)}</dd>
                </div>
              </dl>
            </section>

            <nav className={styles.viewNav} aria-label="Разделы соревнования">
              {viewItems.map((item) => (
                <Link
                  aria-current={view === item.view ? "page" : undefined}
                  className={styles.viewLink}
                  href={buildViewHref(item.view)}
                  key={item.view}
                  onClick={(event) => {
                    if (
                      event.defaultPrevented ||
                      event.button !== 0 ||
                      event.metaKey ||
                      event.ctrlKey ||
                      event.shiftKey ||
                      event.altKey
                    ) {
                      return;
                    }
                    event.preventDefault();
                    selectView(item.view);
                  }}
                >
                  {item.label}
                </Link>
              ))}
            </nav>

            <aside className={styles.participationSlot} aria-label="Участие в соревновании">
              <TestBotsPanel tournamentId={loadState.item.tournamentId} />
              <TournamentEntry
                key={loadState.item.tournamentId}
                publicId={loadState.item.publicId}
                catalogReturnPath={returnHref}
                returnPath={buildViewHref(view)}
                admissionRefreshVersion={admissionRefreshVersion}
                onRosterSizeChange={handleEntryRosterSizeChange}
                state={loadState.item.state}
                tournamentId={loadState.item.tournamentId}
              />
            </aside>

            {isPrestartItem(loadState.item) ? (
              <Message tone="info" title="Соревнование готовится к старту">
                <p>Матчи и таблица появятся после старта соревнования.</p>
              </Message>
            ) : (
              <PublicBroadcast
                item={loadState.item}
                onRosterSizeChange={handleRosterSizeChange}
                onTournamentMissing={handleTournamentMissing}
                view={view}
              />
            )}
          </>
        )}
      </div>
    </ArenaShell>
  );
};

ArenaPublicTournamentPage.displayName = "ArenaPublicTournamentPage";
