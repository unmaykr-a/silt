import { describe, it, expect } from "vitest";
import { answer, fixtureKey, matchTimeline, rebase, shiftTimes, type Fixtures } from "./demo";

const fixtures = {
  "/api/projects": [{ id: 1, name: "media" }],
  "/api/diff?from=1&to=5": { changes: [] },
};

describe("fixtureKey", () => {
  it("keeps the path", () => {
    expect(fixtureKey("/api/projects")).toBe("/api/projects");
  });

  it("sorts query parameters", () => {
    // The UI builds them in a fixed order; a link someone shares may not.
    expect(fixtureKey("/api/diff?to=5&from=1")).toBe("/api/diff?from=1&to=5");
    expect(fixtureKey("/api/diff?from=1&to=5")).toBe("/api/diff?from=1&to=5");
  });

  it("drops nothing that changes the answer", () => {
    expect(fixtureKey("/api/search?q=radarr")).toBe("/api/search?q=radarr");
  });
});

describe("answer", () => {
  it("serves a captured response", async () => {
    const res = answer(fixtures, "/api/projects", "GET");
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual([{ id: 1, name: "media" }]);
  });

  it("matches regardless of parameter order", async () => {
    expect(answer(fixtures, "/api/diff?to=5&from=1", "GET").status).toBe(200);
  });

  it("refuses every write rather than appearing to work", async () => {
    for (const method of ["POST", "PUT", "DELETE", "PATCH"]) {
      const res = answer(fixtures, "/api/settings", method);
      expect(res.status).toBe(503);
      expect((await res.json()).error).toContain("read-only demo");
    }
  });

  it("404s a request the capture never reached", async () => {
    // Honest, and lets the screen show its own empty state rather than hang.
    const res = answer(fixtures, "/api/projects/999", "GET");
    expect(res.status).toBe(404);
  });
});

describe("shiftTimes", () => {
  it("moves timestamps and leaves everything else alone", () => {
    const t = 1_700_000_000_000;
    const out = shiftTimes(
      { id: 7, last_seen_at: t, ts: t, bucket_ms: 360_000, name: "media", running: 3 },
      1000,
    );
    expect(out).toEqual({
      id: 7,
      last_seen_at: t + 1000,
      ts: t + 1000,
      bucket_ms: 360_000,
      name: "media",
      running: 3,
    });
  });

  it("leaves small numbers in time fields alone", () => {
    // /api/diff?from=1&to=5 carries snapshot ids under those names. Shifting
    // one would turn a diff into a lookup for a snapshot that never existed.
    expect(shiftTimes({ from: 1, to: 5 }, 1000)).toEqual({ from: 1, to: 5 });
  });

  it("does not mutate its input", () => {
    // The fixture object is shared between requests: mutating it would
    // compound the shift on every call.
    const input = { taken_at: 1_700_000_000_000 };
    shiftTimes(input, 5000);
    expect(input.taken_at).toBe(1_700_000_000_000);
  });

  it("reaches through arrays and nesting", () => {
    const t = 1_700_000_000_000;
    const out = shiftTimes({ buckets: [{ start: t, changes: 2 }], meta: { ts: t } }, 10);
    expect(out).toEqual({ buckets: [{ start: t + 10, changes: 2 }], meta: { ts: t + 10 } });
  });

  it("is a no-op at zero offset", () => {
    const input = { ts: 1_700_000_000_000 };
    expect(shiftTimes(input, 0)).toBe(input);
  });
});

describe("rebase", () => {
  it("puts the capture on the reader's clock", () => {
    const captured = 1_700_000_000_000;
    const now = captured + 86_400_000;
    const out = rebase({ __captured_at: captured, "/api/events": [{ ts: captured - 60_000 }] }, now);
    expect(out["/api/events"]).toEqual([{ ts: now - 60_000 }]);
  });

  it("drops the stamp from the served set", () => {
    expect(rebase({ __captured_at: 1, "/api/version": {} }, 2)).not.toHaveProperty("__captured_at");
  });

  it("leaves an unstamped capture where it is", () => {
    const t = 1_700_000_000_000;
    expect(rebase({ "/api/events": [{ ts: t }] }, t + 99)).toEqual({ "/api/events": [{ ts: t }] });
  });
});

describe("matchTimeline", () => {
  const now = 1_700_000_000_000;
  const tl = (from: number, to: number, project?: number) => ({
    from,
    to,
    bucket_ms: (to - from) / 2,
    buckets: [
      { start: from, changes: 1 },
      { start: (from + to) / 2, changes: 4 },
    ],
    ...(project ? { project } : {}),
  });
  const set: Fixtures = {
    [`/api/timeline?from=${now - 3_600_000}&to=${now}`]: tl(now - 3_600_000, now),
    [`/api/timeline?from=${now - 86_400_000}&to=${now}`]: tl(now - 86_400_000, now),
    [`/api/timeline?from=${now - 86_400_000}&project=3&to=${now}`]: tl(now - 86_400_000, now, 3),
  };

  it("answers a window nobody captured with the nearest range", () => {
    const later = now + 600_000;
    const out = matchTimeline(set, `/api/timeline?from=${later - 3_600_000}&to=${later}`) as any;
    expect(out.to).toBe(later);
    expect(out.from).toBe(later - 3_600_000);
    // The histogram keeps its shape; only its position moves.
    expect(out.buckets.map((b: any) => b.changes)).toEqual([1, 4]);
    expect(out.buckets[0].start).toBe(later - 3_600_000);
  });

  it("keeps the project filter", () => {
    const out = matchTimeline(set, `/api/timeline?from=${now - 90_000_000}&project=3&to=${now}`) as any;
    expect(out.project).toBe(3);
  });

  it("does not answer a project request from the unfiltered capture", () => {
    expect(matchTimeline(set, `/api/timeline?from=${now - 3_600_000}&project=9&to=${now}`)).toBeNull();
  });

  it("ignores paths that are not the timeline", () => {
    expect(matchTimeline(set, "/api/events?limit=100")).toBeNull();
  });

  it("declines a request with no window", () => {
    expect(matchTimeline(set, "/api/timeline")).toBeNull();
  });

  it("is reached through answer() when no exact key matches", async () => {
    const later = now + 1000;
    const res = answer(set, `/api/timeline?from=${later - 86_400_000}&to=${later}`, "GET");
    expect(res.status).toBe(200);
    expect((await res.json()).to).toBe(later);
  });
});

describe("answer, text fixtures", () => {
  it("serves captured YAML unquoted", async () => {
    const res = answer({ "/api/snapshots/1/compose?format=yaml": "services:\n  web: {}\n" }, "/api/snapshots/1/compose?format=yaml", "GET");
    expect(await res.text()).toBe("services:\n  web: {}\n");
  });
});

describe("answer, captured errors", () => {
  it("replays the status and body the server gave", async () => {
    // The demo mounts no compose roots, so a file preview really is a 503.
    // Replaying it shows the screen's own explanation instead of the shim's
    // "no demo data", which would be a lie about why the panel is empty.
    const set = {
      "/api/projects/1/files/preview?path=/srv/a/compose.yaml": {
        __status: 503,
        __body: { error: "no compose roots are configured" },
      },
    };
    const res = answer(set, "/api/projects/1/files/preview?path=/srv/a/compose.yaml", "GET");
    expect(res.status).toBe(503);
    expect((await res.json()).error).toContain("compose roots");
  });

  it("still 404s a key that was never captured", () => {
    expect(answer({}, "/api/projects/1/files/preview?path=/x", "GET").status).toBe(404);
  });
});
