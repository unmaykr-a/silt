/**
 * Stop an unusable browser locale from taking the whole page down.
 *
 * A browser started under `LANG=C`, `LANG=POSIX` or `LANG=en_US@posix` reports
 * `navigator.language` as `en-US@posix`, which is not a valid BCP 47 tag.
 * uPlot builds a formatter from it as it loads:
 *
 *     const numFormatter = new Intl.NumberFormat(domEnv ? nav.language : 'en-US');
 *
 * Module scope, so importing uPlot throws `RangeError: Invalid language tag`,
 * so the bundle that imports it throws, so the page is blank — not a
 * wrong-looking axis, nothing at all, from a locale setting that has nothing
 * to do with charts.
 *
 * The fix guards the two Intl constructors rather than patching the navigator.
 * A page or extension can define `navigator.language` non-configurably, and a
 * repair that cannot be applied is no repair; `Intl` is a plain writable
 * global. A Proxy keeps `instanceof`, the statics and calling without `new`
 * intact, and only does anything at all on the throw — a browser with a valid
 * locale runs the original constructor and nothing else.
 *
 * Our own formatting is covered separately, by locale.ts: `toLocaleString`
 * does not go through these constructors.
 *
 * See PROJECT.md Section 15.
 */

/** What stands in when nothing can be salvaged. */
export const FALLBACK_TAG = "en-US";

/**
 * A usable form of `tag`, or null if there is nothing to salvage.
 *
 * The `@modifier` suffix is the whole problem in practice — `en-US@posix` is
 * `en-US` with a POSIX marker glued on — so dropping it is what gets tried.
 */
export function sanitiseTag(tag: unknown): string | null {
  if (typeof tag !== "string" || !tag) return null;
  for (const candidate of [tag, tag.split("@")[0]]) {
    if (!candidate) continue;
    try {
      // Throws on a malformed tag; returns [] for an empty one.
      if (Intl.getCanonicalLocales(candidate).length > 0) return candidate;
    } catch {
      // Try the next candidate.
    }
  }
  return null;
}

/**
 * The locales argument to retry a failed construction with.
 *
 * `undefined` means "the browser's own", so a throw on undefined is the
 * browser's own locale being the broken one: name a valid tag instead.
 */
export function sanitiseLocales(locales: unknown): string | string[] {
  if (locales === undefined || locales === null) return FALLBACK_TAG;
  if (Array.isArray(locales)) {
    const kept = locales.map(sanitiseTag).filter((t): t is string => t !== null);
    return kept.length ? kept : FALLBACK_TAG;
  }
  return sanitiseTag(locales) ?? FALLBACK_TAG;
}

type Ctor = new (...args: unknown[]) => unknown;

/**
 * Wrap one Intl constructor so an invalid locale is retried, not thrown.
 *
 * Exported for testing against a stand-in constructor: the real ones cannot be
 * made to fail on demand.
 */
export function guarded<T extends Ctor>(original: T): T {
  const retry = (fn: (args: unknown[]) => unknown, args: unknown[]): unknown => {
    try {
      return fn(args);
    } catch (err) {
      // Only a bad locale is recoverable. A bad options object is a real
      // programming error and must keep throwing.
      if (!(err instanceof RangeError)) throw err;
      return fn([sanitiseLocales(args[0]), ...args.slice(1)]);
    }
  };

  return new Proxy(original, {
    construct: (target, args, newTarget) =>
      retry((a) => Reflect.construct(target, a, newTarget), args) as object,
    apply: (target, thisArg, args) =>
      retry((a) => Reflect.apply(target as unknown as (...p: unknown[]) => unknown, thisArg, a), args),
  });
}

/** Apply the guard to the real Intl. Safe to call more than once. */
export function guardIntl(target: typeof Intl = Intl): void {
  for (const name of ["NumberFormat", "DateTimeFormat"] as const) {
    const original = target[name] as unknown as Ctor | undefined;
    if (typeof original !== "function") continue;
    try {
      (target as unknown as Record<string, unknown>)[name] = guarded(original);
    } catch {
      // A frozen Intl leaves us where we started, which is no worse than not
      // having tried.
    }
  }
}

guardIntl();
