export type ArenaProtectedRole = "participant" | "operator";
export type ArenaPublicView = "overview" | "matches" | "bracket" | "standings";

const LOCAL_ORIGIN = "https://arena.local";
const TOURNAMENT_ID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const PUBLIC_TOURNAMENT_ID_PATTERN =
  /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const PUBLIC_VIEW_VALUES: readonly ArenaPublicView[] = [
  "overview",
  "matches",
  "bracket",
  "standings",
];

const isHttpProtocol = (protocol: string): boolean =>
  protocol === "http:" || protocol === "https:";

/** Resolve a server-provided task URL without allowing a browser boundary escape. */
export const getSafeTaskHref = (
  value: string | null | undefined,
  currentOrigin?: string,
): string | null => {
  if (value === null || value === undefined) {
    return null;
  }

  const candidate = value.trim();
  if (
    candidate.length === 0 ||
    candidate.startsWith("//") ||
    candidate.includes("\\") ||
    candidate.includes("#")
  ) {
    return null;
  }

  const rootRelative = candidate.startsWith("/");
  const explicitHttp = /^https?:\/\//i.test(candidate);
  if (!rootRelative && !explicitHttp) {
    return null;
  }

  let page: URL;
  try {
    page = new URL(
      currentOrigin ?? (typeof window === "undefined" ? LOCAL_ORIGIN : window.location.origin),
    );
  } catch {
    return null;
  }

  let resolved: URL;
  try {
    resolved = rootRelative ? new URL(candidate, page) : new URL(candidate);
  } catch {
    return null;
  }

  if (
    !isHttpProtocol(resolved.protocol) ||
    resolved.username.length > 0 ||
    resolved.password.length > 0 ||
    resolved.hash.length > 0 ||
    (page.protocol === "https:" && resolved.protocol === "http:")
  ) {
    return null;
  }

  return candidate;
};

export const getSafeArenaReturnPath = (
  value: string | null | undefined,
  role: ArenaProtectedRole,
): string | null => {
  if (!value || value.startsWith("//") || value.includes("\\")) {
    return null;
  }

  try {
    const url = new URL(value, LOCAL_ORIGIN);
    const prefix = `/arena/${role}/`;
    const tournamentId = url.pathname.startsWith(prefix)
      ? url.pathname.slice(prefix.length)
      : "";

    if (
      url.origin !== LOCAL_ORIGIN ||
      !TOURNAMENT_ID_PATTERN.test(tournamentId)
    ) {
      return null;
    }

    return `${url.pathname}${url.search}${url.hash}`;
  } catch {
    return null;
  }
};

export const isSafePublicTournamentId = (value: string): boolean =>
  PUBLIC_TOURNAMENT_ID_PATTERN.test(value) && value.length <= 64;

export const isArenaPublicView = (value: string | null | undefined): value is ArenaPublicView =>
  value !== null &&
  value !== undefined &&
  PUBLIC_VIEW_VALUES.includes(value as ArenaPublicView);

const isSafePublicQuery = (
  url: URL,
  allowNestedReturn = false,
  nestedDepth = 0,
): boolean => {
  const allowedKeys = new Set(["q", "group", "sort", "cursor", "view", "match"]);
  if (allowNestedReturn) {
    allowedKeys.add("return");
  }
  for (const key of Array.from(url.searchParams.keys())) {
    if (!allowedKeys.has(key) || url.searchParams.getAll(key).length !== 1) {
      return false;
    }
  }
  const view = url.searchParams.get("view");
  if (view !== null && !isArenaPublicView(view)) {
    return false;
  }
  const nestedReturn = url.searchParams.get("return");
  if (nestedReturn === null) {
    return true;
  }
  if (!allowNestedReturn || url.searchParams.getAll("return").length !== 1) {
    return false;
  }
  try {
    const nestedURL = new URL(nestedReturn, LOCAL_ORIGIN);
    if (
      nestedURL.origin !== LOCAL_ORIGIN ||
      nestedURL.hash !== ""
    ) {
      return false;
    }
    if (nestedURL.pathname === "/arena") {
      return !nestedURL.searchParams.has("return") && isSafePublicQuery(nestedURL);
    }
    if (nestedDepth >= 1) {
      return false;
    }
    const detailPrefix = "/arena/tournaments/";
    const nestedPublicId = nestedURL.pathname.startsWith(detailPrefix)
      ? nestedURL.pathname.slice(detailPrefix.length)
      : "";
    return isSafePublicTournamentId(nestedPublicId) &&
      isSafePublicQuery(nestedURL, true, nestedDepth + 1);
  } catch {
    return false;
  }
};

export const getSafeArenaPublicReturnPath = (
  value: string | null | undefined,
): string | null => {
  if (!value || value.startsWith("//") || value.includes("\\") || value.length > 4096) {
    return null;
  }

  try {
    const url = new URL(value, LOCAL_ORIGIN);
    if (url.origin !== LOCAL_ORIGIN || url.hash) {
      return null;
    }
    if (url.pathname === "/arena") {
      return isSafePublicQuery(url) ? `${url.pathname}${url.search}` : null;
    }
    const prefix = "/arena/tournaments/";
    const publicId = url.pathname.startsWith(prefix)
      ? url.pathname.slice(prefix.length)
      : "";
    if (!isSafePublicTournamentId(publicId) || !isSafePublicQuery(url, true)) {
      return null;
    }
    return `${url.pathname}${url.search}`;
  } catch {
    return null;
  }
};

export const buildArenaPublicTournamentPath = (
  publicId: string,
  view: ArenaPublicView = "overview",
  returnPath?: string | null,
): string => {
  if (!isSafePublicTournamentId(publicId)) {
    return "/arena";
  }
  const params = new URLSearchParams({ view });
  const safeReturnPath = getSafeArenaPublicReturnPath(returnPath);
  if (safeReturnPath) {
    params.set("return", safeReturnPath);
  }
  return `/arena/tournaments/${encodeURIComponent(publicId)}?${params.toString()}`;
};
