"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import type {
  ParticipantDraftIntent,
  ParticipantDraftResult,
  ParticipantDraftView as ParticipantDraftSnapshotView,
  ParticipantPlayerView,
} from "./model";

export type ParticipantDraftStatus =
  | "idle"
  | "submitting"
  | "accepted"
  | "conflict"
  | "rate_limited"
  | "error";

type ParticipantDraftState = Readonly<{
  message: string | null;
  status: ParticipantDraftStatus;
}>;

type ParticipantDraftControls = Readonly<{
  allowed: boolean;
  act: (category: ParticipantDraftCategory) => void;
  ban: (category: ParticipantDraftCategory) => void;
  pick: (category: ParticipantDraftCategory) => void;
  message: string | null;
  status: ParticipantDraftStatus;
}>;

type ParticipantDraftCategory = ParticipantDraftSnapshotView["pool"][number];

type UseParticipantDraftOptions = Readonly<{
  onDraft: (intent: ParticipantDraftIntent) => Promise<ParticipantDraftResult>;
  view: ParticipantPlayerView | null;
}>;

const initialState: ParticipantDraftState = {
  message: null,
  status: "idle",
};

const isBo1Pool = (pool: readonly string[]): boolean => {
  const allowed = new Set(["web", "crypto", "reverse"]);
  return pool.length === 3 && pool.every((category) => allowed.has(category));
};

const isBo3Pool = (pool: readonly string[]): boolean => {
  const allowed = new Set(["web", "crypto", "reverse", "forensics", "pwn"]);
  return pool.length === 5 && pool.every((category) => allowed.has(category));
};

const draftHasSupportedPool = (
  format: ParticipantDraftSnapshotView["format"],
  pool: readonly string[],
): boolean => format === "bo1" ? isBo1Pool(pool) : isBo3Pool(pool);

const draftCanAcceptAction = (view: ParticipantPlayerView | null): boolean => {
  const draft = view?.draft ?? null;
  return (
    view !== null &&
    draft !== null &&
    draftHasSupportedPool(draft.format, draft.pool) &&
    draft.state === "active" &&
    draft.currentAction !== null &&
    draft.currentActorId === view.participantId &&
    draft.turnDeadline !== null &&
    draft.legalCategories.length > 0
  );
};

const draftTokenFor = (view: ParticipantPlayerView | null): string => {
  const draft = view?.draft ?? null;
  if (view === null || draft === null) {
    return "none";
  }
  return [
    view.tournamentId,
    view.participantId,
    view.projectionRevision,
    draft.id,
    draft.revision,
    draft.turn,
    draft.currentActorId,
    draft.currentAction,
    draft.turnDeadline,
    JSON.stringify(draft.legalCategories),
    draft.state,
  ].join(":");
};

const defaultMessageFor = (status: ParticipantDraftResult["status"]): string => {
  switch (status) {
    case "accepted":
      return "Ход принят сервером.";
    case "conflict":
      return "Драфт изменился. Сверяем историю с сервером.";
    case "rate_limited":
      return "Слишком много ходов. Повторите после паузы.";
  }
};

/**
 * Sends only a server-authorized action. The hook never adds a local action
 * to the history and never creates a timeout action; recovery remains the
 * source of truth for every subsequent turn.
 */
export const useParticipantDraft = ({
  onDraft,
  view,
}: UseParticipantDraftOptions): ParticipantDraftControls => {
  const draft = view?.draft ?? null;
  const draftToken = draftTokenFor(view);
  const [state, setState] = useState<ParticipantDraftState>(initialState);
  const inFlightRef = useRef(false);
  const requestRef = useRef(0);
  const tokenRef = useRef(draftToken);

  useEffect(() => {
    if (tokenRef.current === draftToken) {
      return;
    }

    tokenRef.current = draftToken;
    requestRef.current += 1;
    inFlightRef.current = false;
    setState(initialState);
  }, [draftToken]);

  const act = useCallback((category: ParticipantDraftCategory) => {
    if (
      view === null ||
      draft === null ||
      !draftCanAcceptAction(view) ||
      draft.currentAction === null ||
      !draft.legalCategories.includes(category) ||
      inFlightRef.current
    ) {
      return;
    }

    inFlightRef.current = true;
    const requestId = ++requestRef.current;
    const requestToken = draftToken;
    const intent: ParticipantDraftIntent = {
      action: draft.currentAction,
      category,
      draftId: draft.id,
      draftRevision: draft.revision,
      expectedTurn: draft.turn,
      projectionRevision: view.projectionRevision,
      seriesId: draft.seriesId,
      tournamentId: view.tournamentId,
    };
    setState({
      message: draft.currentAction === "ban"
        ? "Передаем бан серверу."
        : "Передаем выбор серверу.",
      status: "submitting",
    });

    void onDraft(intent)
      .then((result) => {
        if (requestId !== requestRef.current || requestToken !== tokenRef.current) {
          return;
        }
        setState({
          message: result.message ?? defaultMessageFor(result.status),
          status: result.status,
        });
      })
      .catch((error: unknown) => {
        if (requestId !== requestRef.current || requestToken !== tokenRef.current) {
          return;
        }
        setState({
          message: error instanceof Error
            ? error.message
            : "Не удалось передать ход серверу. Повторите попытку.",
          status: "error",
        });
      })
      .finally(() => {
        if (requestId === requestRef.current && requestToken === tokenRef.current) {
          inFlightRef.current = false;
        }
      });
  }, [draft, draftToken, onDraft, view]);

  const ban = useCallback((category: ParticipantDraftCategory) => {
    if (draft?.currentAction === "ban") {
      act(category);
    }
  }, [act, draft?.currentAction]);

  const pick = useCallback((category: ParticipantDraftCategory) => {
    if (draft?.currentAction === "pick") {
      act(category);
    }
  }, [act, draft?.currentAction]);

  return {
    act,
    ban,
    pick,
    allowed: draftCanAcceptAction(view),
    message: state.message,
    status: state.status,
  };
};
