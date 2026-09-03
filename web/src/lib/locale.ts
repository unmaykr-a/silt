/**
 * Choosing a locale the browser will actually accept.
 *
 * Passing `undefined` to `toLocaleString` means "use the browser's own
 * locale", which is normally exactly right — someone who has told their OS
 * they want dd/mm should not have to tell Silt as well — and occasionally is
 * not a locale at all. A Chromium started under `LANG=en_US@posix` reports its
 * locale as `en-US@posix`, which is not a valid BCP 47 tag, and every
 * `toLocaleString` on the page throws `RangeError: Invalid language tag`.
 *
 * Date formatting is on nearly every screen, so that is not a wrong-looking
 * timestamp: it is a blank page. One environment variable on the host takes
 * down the whole UI, and nothing in the app is at fault.
 *
 * So the system locale is probed once and, if it cannot be used, a fixed one
 * stands in. An explicitly chosen locale needs no guard — those are constants
 * in this codebase and always valid.
 */

/** What to use when the browser's own locale is unusable. */
export const FALLBACK_LOCALE = "en-GB";

/** Probe a formatter. Split out so a test can supply a failing one. */
export type Probe = () => void;

const defaultProbe: Probe = () => {
  new Date(0).toLocaleString(undefined, { hour: "2-digit", minute: "2-digit" });
};

/**
 * Resolve the locale to pass to a `toLocale*` call.
 *
 * `requested` of undefined means the browser's own. The probe runs at most
 * once per resolver: it is the same answer every time, and formatting happens
 * on every row of the timeline.
 */
export function createLocaleResolver(probe: Probe = defaultProbe) {
  let systemUsable: boolean | null = null;

  return function localeFor(requested: string | undefined): string | undefined {
    if (requested !== undefined) return requested;
    if (systemUsable === null) {
      try {
        probe();
        systemUsable = true;
      } catch {
        systemUsable = false;
      }
    }
    return systemUsable ? undefined : FALLBACK_LOCALE;
  };
}

/** The resolver the app uses. */
export const localeFor = createLocaleResolver();
