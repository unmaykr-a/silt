/**
 * Per-viewer display preferences.
 *
 * These live in the browser, not in Silt's settings, and that is deliberate:
 * a 24-hour clock or a dd/mm/yyyy date is a property of the person reading the
 * screen, not of the install. Two people looking at the same Silt should each
 * get their own, and neither should be able to change the other's.
 */

export type Layout = "top" | "side";
export type Clock = "system" | "h24" | "h12";
export type DateStyle = "system" | "dmy" | "mdy" | "ymd";
export type TimeStamps = "relative" | "absolute";

export type Prefs = {
  layout: Layout;
  clock: Clock;
  dateStyle: DateStyle;
  timestamps: TimeStamps;
  /** Seconds in timestamps. Off by default; on when you are reading an incident. */
  seconds: boolean;
};

const DEFAULTS: Prefs = {
  layout: "top",
  clock: "system",
  dateStyle: "system",
  timestamps: "relative",
  seconds: false,
};

const STORAGE_KEY = "silt.prefs";

function stored(): Partial<Prefs> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as Partial<Prefs>;
    return typeof parsed === "object" && parsed !== null ? parsed : {};
  } catch {
    // Private windows, blocked site data, and a document written by an older
    // version all land here. Defaults are always a working answer.
    return {};
  }
}

function createPrefs() {
  let value = $state<Prefs>({ ...DEFAULTS, ...stored() });

  function persist() {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(value));
    } catch {
      // Non-fatal: the choice just will not survive a reload.
    }
  }

  return {
    get value() {
      return value;
    },
    get layout() {
      return value.layout;
    },
    get clock() {
      return value.clock;
    },
    get dateStyle() {
      return value.dateStyle;
    },
    get timestamps() {
      return value.timestamps;
    },
    get seconds() {
      return value.seconds;
    },
    set<K extends keyof Prefs>(key: K, next: Prefs[K]) {
      value = { ...value, [key]: next };
      persist();
    },
    reset() {
      value = { ...DEFAULTS };
      persist();
    },
  };
}

export const prefs = createPrefs();

/**
 * Intl options for a date, built from the chosen style.
 *
 * `system` returns undefined so the browser's own locale decides, which is the
 * right default: someone who has already told their OS they want dd/mm should
 * not have to tell Silt as well.
 */
export function dateParts(style: DateStyle): Intl.DateTimeFormatOptions | undefined {
  switch (style) {
    case "dmy":
    case "mdy":
      return { day: "2-digit", month: "2-digit", year: "numeric" };
    case "ymd":
      return { year: "numeric", month: "2-digit", day: "2-digit" };
    default:
      return undefined;
  }
}

/** The locale that renders a given date order. */
export function dateLocale(style: DateStyle): string | undefined {
  switch (style) {
    case "dmy":
      return "en-GB";
    case "mdy":
      return "en-US";
    case "ymd":
      return "en-CA"; // yyyy-mm-dd
    default:
      return undefined;
  }
}
