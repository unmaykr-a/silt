/**
 * Demo mode: Silt running with no server behind it.
 *
 * The published demo is a static site, so there is nothing to answer /api.
 * This installs a fetch shim that serves responses captured from a real Silt
 * running against the demo database — the same data `make demo` produces.
 *
 * Deliberately a shim over `fetch` rather than a second API client. A parallel
 * client would drift from the real one, and the first thing anyone would learn
 * from the demo is how the demo behaves. This way every screen runs exactly
 * the code it runs in production, and only the transport is different.
 *
 * Two things the capture cannot give directly, and this file supplies:
 *
 *  - Time. A capture is stamped at one instant; a demo is read for months.
 *    Every timestamp is shifted onto the reader's clock at load, so the last
 *    change is always "a few minutes ago" rather than a date that grows
 *    steadily more embarrassing.
 *  - Timeline windows. The two range pickers ask for `from`/`to` derived from
 *    `Date.now()`, which no fixed key can match. The nearest captured range
 *    answers instead, re-stamped onto the window that was asked for.
 */

/** True when this bundle was built for the static demo. */
export const IS_DEMO = import.meta.env.VITE_SILT_DEMO === "1";

export type Fixtures = Record<string, unknown>;

/** Writes are refused with this, rather than appearing to work. */
const READ_ONLY = {
  error: "This is a read-only demo. Run Silt on your own host to change anything.",
};

/** Where the capture stamps the instant it ran. */
const CAPTURED_AT = "__captured_at";

/**
 * A captured error response.
 *
 * Silt answers 503 for a file preview when no compose roots are mounted, and
 * the demo has none. Replaying the real answer shows the screen's own
 * explanation; dropping it would show "no demo data", which is a lie about
 * why the panel is empty.
 */
type CapturedError = { __status: number; __body: unknown };

function isCapturedError(v: unknown): v is CapturedError {
  return !!v && typeof v === "object" && typeof (v as CapturedError).__status === "number";
}

/**
 * Normalise a URL to the key the fixtures were captured under.
 *
 * Query parameters are kept but sorted, so `?from=1&to=2` and `?to=2&from=1`
 * are the same request — the UI builds them in a fixed order, but a link
 * someone shares may not.
 */
export function fixtureKey(rawUrl: string, base = "http://demo"): string {
  const url = new URL(rawUrl, base);
  const params = [...url.searchParams.entries()].sort(([a], [b]) => a.localeCompare(b));
  const query = params.map(([k, v]) => `${k}=${v}`).join("&");
  return url.pathname + (query ? `?${query}` : "");
}

/**
 * Field names whose numeric values are wall-clock milliseconds.
 *
 * An allowlist rather than "any number that looks like an epoch", because ids
 * and counts pass that test too and a silently shifted id is a bug nobody
 * would think to look for. `from`, `to` and `start` are only shifted when
 * they are plainly milliseconds — as snapshot ids they are small numbers.
 */
const TIME_FIELDS = /(_at|_time)$|^(ts|start|from|to|first_seen|last_seen|timestamp)$/;

/** Below this, a number in a time field is an id or an offset, not a clock. */
const EPOCH_FLOOR = 1_000_000_000_000; // 2001-09-09

/**
 * Move every timestamp in a captured response by `offset` milliseconds.
 *
 * Structural copy: the fixture object is shared between requests, so mutating
 * it would compound the shift on every call.
 */
export function shiftTimes<T>(value: T, offset: number): T {
  if (offset === 0) return value;
  return walk(value, offset, false) as T;
}

function walk(value: unknown, offset: number, timeField: boolean): unknown {
  if (typeof value === "number") {
    return timeField && Number.isFinite(value) && value >= EPOCH_FLOOR ? value + offset : value;
  }
  if (Array.isArray(value)) {
    // An array under a time-field name is a list of timestamps, so the flag
    // carries through it rather than being dropped at the bracket.
    return value.map((v) => walk(v, offset, timeField));
  }
  if (value && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      out[k] = walk(v, offset, TIME_FIELDS.test(k));
    }
    return out;
  }
  return value;
}

type TimelineBody = {
  from: number;
  to: number;
  bucket_ms: number;
  buckets: { start: number; [k: string]: unknown }[];
};

function isTimeline(v: unknown): v is TimelineBody {
  const t = v as TimelineBody;
  return !!t && typeof t.from === "number" && typeof t.to === "number" && Array.isArray(t.buckets);
}

/**
 * Answer a timeline request from the nearest captured window.
 *
 * The captures were taken at ranges the pickers actually offer, so "nearest"
 * is normally exact in duration and off only by the minutes between the
 * capture and the visit. That residual is closed by sliding the buckets, which
 * keeps the histogram's shape and puts its right edge at the requested `to`.
 *
 * `fixtures` must already be shifted onto the current clock.
 */
export function matchTimeline(fixtures: Fixtures, key: string): unknown | null {
  const url = new URL(key, "http://demo");
  if (url.pathname !== "/api/timeline") return null;

  const wantProject = url.searchParams.get("project") ?? "";
  const wantFrom = Number(url.searchParams.get("from"));
  const wantTo = Number(url.searchParams.get("to"));
  if (!Number.isFinite(wantFrom) || !Number.isFinite(wantTo) || wantTo <= wantFrom) return null;
  const wantSpan = wantTo - wantFrom;

  let best: TimelineBody | null = null;
  let bestDelta = Infinity;
  for (const [k, body] of Object.entries(fixtures)) {
    const u = new URL(k, "http://demo");
    if (u.pathname !== "/api/timeline") continue;
    if ((u.searchParams.get("project") ?? "") !== wantProject) continue;
    if (!isTimeline(body)) continue;
    // Compare on the span the server actually served, not the one requested:
    // it clamps, and a clamped window is what the buckets describe.
    const delta = Math.abs(body.to - body.from - wantSpan);
    if (delta < bestDelta) {
      best = body;
      bestDelta = delta;
    }
  }
  if (!best) return null;

  const slide = wantTo - best.to;
  return {
    ...best,
    from: best.from + slide,
    to: wantTo,
    buckets: best.buckets.map((b) => ({ ...b, start: b.start + slide })),
  };
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

/**
 * Answer one API request from the fixtures.
 *
 * Exported for testing: the lookup rules are the part worth pinning, not the
 * fetch plumbing around them.
 */
export function answer(fixtures: Fixtures, url: string, method: string): Response {
  if (method !== "GET") return json(READ_ONLY, 503);

  const key = fixtureKey(url);
  if (key in fixtures) {
    const body = fixtures[key];
    if (isCapturedError(body)) return json(body.__body ?? {}, body.__status);
    // Compose YAML is captured as text and must not come back quoted.
    if (typeof body === "string") {
      return new Response(body, { status: 200, headers: { "Content-Type": "text/plain" } });
    }
    return json(body);
  }

  const timeline = matchTimeline(fixtures, key);
  if (timeline) return json(timeline);

  // A request the capture did not reach. Answering 404 is honest and lets the
  // screen show its own empty state rather than hanging.
  return json({ error: `No demo data for ${key}` }, 404);
}

/** Shift a whole fixture set onto `now`. Exported for testing. */
export function rebase(raw: Fixtures, now = Date.now()): Fixtures {
  const capturedAt = typeof raw[CAPTURED_AT] === "number" ? (raw[CAPTURED_AT] as number) : 0;
  const offset = capturedAt ? now - capturedAt : 0;
  const out: Fixtures = {};
  for (const [k, v] of Object.entries(raw)) {
    if (k === CAPTURED_AT) continue;
    out[k] = shiftTimes(v, offset);
  }
  return out;
}

/**
 * Replace window.fetch for /api paths. Call once, before the app mounts.
 *
 * The fixtures are fetched rather than imported so the file stays a build
 * artefact: `make demo-site` drops it next to the bundle, and a normal build
 * neither carries it nor needs it to exist.
 */
export async function installDemoFetch(): Promise<void> {
  if (!IS_DEMO) return;

  const real = window.fetch.bind(window);
  const base = (import.meta.env.BASE_URL || "/").replace(/\/+$/, "");
  const res = await real(`${base}/demo-fixtures.json`, { headers: { Accept: "application/json" } });
  if (!res.ok) throw new Error(`demo fixtures: HTTP ${res.status}`);
  const fixtures = rebase((await res.json()) as Fixtures);

  window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const href = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const method = (init?.method ?? (input instanceof Request ? input.method : "GET")).toUpperCase();

    // Strip the mount point, so /silt/api/projects finds /api/projects.
    const parsed = new URL(href, location.origin);
    const apiAt = parsed.pathname.indexOf("/api/");
    if (apiAt < 0) return real(input as RequestInfo, init);

    return answer(fixtures, parsed.pathname.slice(apiAt) + parsed.search, method);
  };
}
