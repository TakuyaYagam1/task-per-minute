export type ArenaProtectedRole = "participant" | "operator";

const LOCAL_ORIGIN = "https://arena.local";
const TOURNAMENT_ID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

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
