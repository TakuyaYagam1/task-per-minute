const trimTrailingSlash = (value: string | undefined): string =>
  (value || "").replace(/\/+$/, "");

const normalizeExplicitWebSocketUrl = (value: string | undefined): string => {
  const explicit = trimTrailingSlash(value);
  if (!explicit) {
    return "";
  }

  try {
    const parsed = new URL(explicit);
    if ((parsed.protocol === "ws:" || parsed.protocol === "wss:") && parsed.pathname === "/") {
      parsed.pathname = "/ws";
      return trimTrailingSlash(parsed.toString());
    }
  } catch {
    return explicit;
  }

  return explicit;
};

const browserOrigin = (): string => {
  if (typeof window === "undefined") {
    return "http://localhost:3000";
  }
  return window.location.origin;
};

const resolveWebSocketUrl = (): string => {
  const explicit = normalizeExplicitWebSocketUrl(process.env.NEXT_PUBLIC_WS_URL);
  if (explicit) {
    return explicit;
  }

  const apiOrigin = trimTrailingSlash(process.env.NEXT_PUBLIC_API_URL);
  if (apiOrigin.startsWith("https://")) {
    return `wss://${apiOrigin.slice("https://".length)}/ws`;
  }
  if (apiOrigin.startsWith("http://")) {
    return `ws://${apiOrigin.slice("http://".length)}/ws`;
  }

  const origin = browserOrigin();
  return `${origin.startsWith("https://") ? "wss" : "ws"}://${new URL(origin).host}/ws`;
};

export const CONFIG = {
  blockWidth: "900px",
  apiUrl: trimTrailingSlash(process.env.NEXT_PUBLIC_API_URL),
  adminApiUrl: trimTrailingSlash(
    process.env.NEXT_PUBLIC_ADMIN_API_URL || process.env.NEXT_PUBLIC_API_URL,
  ),
  websocketUrl: resolveWebSocketUrl(),
};
