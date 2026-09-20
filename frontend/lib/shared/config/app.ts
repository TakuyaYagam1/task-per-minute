const trimTrailingSlash = (value: string | undefined): string =>
  (value || "").replace(/\/+$/, "");

const readSourceFileOrigins = (value: string | undefined): string[] =>
  (value || "")
    .split(",")
    .map((origin) => origin.trim())
    .filter((origin) => origin.length > 0);

export const CONFIG = {
  blockWidth: "900px",
  apiUrl: trimTrailingSlash(process.env.NEXT_PUBLIC_API_URL),
  adminApiUrl: trimTrailingSlash(
    process.env.NEXT_PUBLIC_ADMIN_API_URL || process.env.NEXT_PUBLIC_API_URL,
  ),
  sourceFileOrigins: readSourceFileOrigins(process.env.NEXT_PUBLIC_SOURCE_FILE_ORIGINS),
};
