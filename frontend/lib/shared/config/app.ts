const trimTrailingSlash = (value: string | undefined): string =>
  (value || "").replace(/\/+$/, "");

export const CONFIG = {
  blockWidth: "900px",
  apiUrl: trimTrailingSlash(process.env.NEXT_PUBLIC_API_URL),
  adminApiUrl: trimTrailingSlash(
    process.env.NEXT_PUBLIC_ADMIN_API_URL || process.env.NEXT_PUBLIC_API_URL,
  ),
};
