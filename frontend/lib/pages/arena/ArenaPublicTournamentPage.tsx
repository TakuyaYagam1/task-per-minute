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
} from "../../shared/api";
import {
  buildArenaPublicTournamentPath,
  getSafeArenaPublicReturnPath,
  isArenaPublicView,
  isSafePublicTournamentId,
  type ArenaPublicView,
} from "../../shared/lib";
import { Button, Message, Status } from "../../shared/ui";
import {
  TournamentRecoveryPanel,
  type TournamentRecoveryRenderContext,
} from "../../features/tournament-live";
import { TournamentEntry } from "../../features/tournament-entry";
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
}>;

const initialLoadState: PublicTournamentLoadState = {
  status: "loading",
  item: null,
  error: null,
  metadataStale: false,
};

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
  { view: "standings", label: "Турнирная таблица" },
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

const errorFor = (error: unknown): Pick<PublicTournamentLoadState, "status" | "error"> => {
  if (error instanceof ApiError) {
    if (error.kind === "not_found") {
      return {
        status: "not_found",
        error: "Турнир не найден или больше не публикуется.",
      };
    }
    if (error.kind === "rate_limited") {
      return {
        status: "rate_limited",
        error: "Слишком много запросов. Повторите попытку позже.",
      };
    }
  }
  return {
    status: "error",
    error: "Не удалось загрузить турнир. Повторите попытку.",
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
  view: ArenaPublicView;
}>;

const PublicBroadcast = ({ item, view }: PublicBroadcastProps) => (
  <TournamentRecoveryPanel contentOnly role="spectator" tournamentId={item.tournamentId}>
    {(context) => <PublicBroadcastContent context={context} view={view} />}
  </TournamentRecoveryPanel>
);

const PublicBroadcastContent = ({
  context,
  view,
}: Readonly<{ context: TournamentRecoveryRenderContext; view: ArenaPublicView }>) => {
  const noPublicState = context.recovery === null && context.publicState === null;
  if (noPublicState) {
    if (context.recoveryError?.kind === "not_found") {
      return (
        <Message tone="info" title="Публичное состояние еще не опубликовано">
          <p>Сервер пока не опубликовал матчи и результаты для этого турнира.</p>
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
          title="Не удалось загрузить публичное состояние"
        >
          <p>Матчи и таблица временно недоступны. Повторите попытку.</p>
          <Button type="button" size="small" variant="secondary" onClick={context.retry}>
            Повторить
          </Button>
        </Message>
      );
    }
    return <Status tone="loading">Загрузка публичного состояния</Status>;
  }

  return (
    <>
      {context.recoveryError && (
        <Message
          tone={context.recoveryError.kind === "rate_limited" || context.recoveryError.kind === "transport"
            ? "warning"
            : "error"}
          title="Публичное состояние могло устареть"
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
  const [reloadVersion, setReloadVersion] = useState(0);
  const requestRef = useRef(0);
  const metadataRequestRef = useRef(0);
  const shouldPollMetadata = loadState.status === "ready" && loadState.item !== null;

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

  useEffect(() => {
    const controller = new AbortController();
    const requestId = ++requestRef.current;
    if (!isSafePublicTournamentId(publicId)) {
      setLoadState({
        status: "not_found",
        item: null,
        error: "Турнир не найден.",
        metadataStale: false,
      });
      return () => controller.abort();
    }

    setLoadState((current) => ({
      status: "loading",
      item: current.item?.publicId === publicId ? current.item : null,
      error: null,
      metadataStale: false,
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

    const refreshMetadata = (): void => {
      if (!active || inFlight || document.visibilityState !== "visible") {
        return;
      }
      inFlight = true;
      const requestId = ++metadataRequestRef.current;
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
          setLoadState((current) => (
            current.item?.publicId === publicId
              ? { ...current, metadataStale: true }
              : current
          ));
        })
        .finally(() => {
          inFlight = false;
        });
    };

    const interval = window.setInterval(refreshMetadata, 15_000);
    const handleVisibilityChange = (): void => {
      if (document.visibilityState === "visible") {
        refreshMetadata();
      }
    };
    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      active = false;
      controller.abort();
      window.clearInterval(interval);
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [publicId, reloadVersion, shouldPollMetadata]);

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

  const returnHref = safeReturnPath ?? "/arena";
  const accessStatus = loadState.status === "loading" ? "loading" :
    loadState.status === "ready" ? "ready" :
      loadState.status === "not_found" ? "missing" : "transport";

  return (
    <ArenaShell accessStatus={accessStatus}>
      <div className={styles.page}>
        <Link className={styles.backLink} href={returnHref}>
          Вернуться к каталогу
        </Link>

        {loadState.status === "loading" && loadState.item === null && (
          <Message tone="loading" title="Загрузка турнира">
            <p>Получаем публичные сведения и состояние просмотра.</p>
          </Message>
        )}

        {loadState.error && (
          <Message
            tone={loadState.status === "rate_limited" ? "warning" : "error"}
            title={loadState.status === "not_found" ? "Турнир не найден" : "Турнир недоступен"}
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
        )}

        {loadState.item && (
          <>
            <header className={styles.header}>
              <div className={styles.heading}>
                <p className={styles.eyebrow}>Публичный просмотр</p>
                <h1 className={styles.title}>{loadState.item.name}</h1>
              </div>
              <Status tone={loadState.item.group === "live" ? "live" : "info"}>
                {catalogGroupLabel(loadState.item.group)}
              </Status>
              {loadState.metadataStale && (
                <Status tone="warning">Сведения могли устареть</Status>
              )}
            </header>

            <section className={styles.metadata} aria-label="Сведения о турнире">
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
                  <dd>{catalogRosterLabel(loadState.item)}</dd>
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
                  <dt>Создан</dt>
                  <dd>{catalogCreatedLabel(loadState.item.createdAt)}</dd>
                </div>
              </dl>
            </section>

            <nav className={styles.viewNav} aria-label="Разделы турнира">
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

            <aside className={styles.participationSlot} aria-label="Участие в турнире">
              <TournamentEntry
                key={loadState.item.tournamentId}
                publicId={loadState.item.publicId}
                returnPath={buildViewHref(view)}
                state={loadState.item.state}
                tournamentId={loadState.item.tournamentId}
              />
            </aside>

            {isPrestartItem(loadState.item) ? (
              <Message tone="info" title="Турнир готовится к старту">
                <p>Матчи и таблица появятся после старта турнира.</p>
              </Message>
            ) : (
              <PublicBroadcast
                item={loadState.item}
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
