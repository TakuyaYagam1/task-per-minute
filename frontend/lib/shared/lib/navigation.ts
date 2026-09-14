export type ArenaProtectedRole = "participant" | "operator";

const LOCAL_ORIGIN = "https://arena.local";
const TOURNAMENT_ID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

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
