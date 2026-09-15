/** The duration menus, in milliseconds because that is what the API takes. */
const MINUTE = 60_000;
const HOUR = 3_600_000;
const DAY = 86_400_000;

export const INTERVALS = [
  ["1 minute", MINUTE],
  ["5 minutes", 5 * MINUTE],
  ["15 minutes", 15 * MINUTE],
  ["30 minutes", 30 * MINUTE],
  ["1 hour", HOUR],
  ["6 hours", 6 * HOUR],
] as const;

export const RETENTION_INTERVALS = [
  ["15 minutes", 15 * MINUTE],
  ["1 hour", HOUR],
  ["6 hours", 6 * HOUR],
  ["24 hours", DAY],
] as const;

export const VACUUM_INTERVALS = [
  ["disabled", 0],
  ["weekly", 7 * DAY],
  ["monthly", 30 * DAY],
] as const;

export const SESSION_TTLS = [
  ["1 day", DAY],
  ["7 days", 7 * DAY],
  ["30 days", 30 * DAY],
  ["90 days", 90 * DAY],
] as const;

export const IDLE_TTLS = [
  ["disabled", 0],
  ["12 hours", 12 * HOUR],
  ["24 hours", DAY],
  ["7 days", 7 * DAY],
  ["30 days", 30 * DAY],
] as const;

export const ADMIN_TTLS = [
  ["never expires", 0],
  ["1 hour", HOUR],
  ["12 hours", 12 * HOUR],
  ["24 hours", DAY],
  ["7 days", 7 * DAY],
] as const;
