const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

const USERNAME_PATTERN = /^[a-zA-Z0-9_-]{2,50}$/;

export const isUUID = (value: unknown): value is string =>
  typeof value === "string" && UUID_PATTERN.test(value);

export const isOptionalUUIDOrNull = (
  value: unknown,
): value is string | null | undefined =>
  value === undefined || value === null || isUUID(value);

export const isValidUsername = (value: string): boolean =>
  USERNAME_PATTERN.test(value);
