import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { SETTINGS, SECTIONS, searchSettings, overrideCounts } from "./settingsindex";

describe("searchSettings", () => {
  it("finds a setting by its label", () => {
    expect(searchSettings("vacuum").map((h) => h.name)).toContain("vacuum_interval_ms");
  });

  it("finds a setting by its environment variable", () => {
    // The compose file is where people know these by name, so the variable has
    // to be searchable — SILT_KEEP_KEYS is the string in hand when the
    // question comes up, not "keys kept readable".
    expect(searchSettings("SILT_KEEP_KEYS").map((h) => h.name)).toContain("keep_keys");
    expect(searchSettings("keep_keys").map((h) => h.name)).toContain("keep_keys");
  });

  it("finds a setting by what it is for rather than what it is called", () => {
    expect(searchSettings("ntfy").map((h) => h.name)).toContain("notify_urls");
    expect(searchSettings("authentik").map((h) => h.name)).toContain("trust_proxy_auth");
    expect(searchSettings("prometheus").map((h) => h.name)).toContain("metrics_public");
  });

  it("narrows on every term rather than widening", () => {
    // "notify severity" should not also return every other notification field.
    const hits = searchSettings("notify severity").map((h) => h.name);
    expect(hits).toContain("notify_min_severity");
    expect(hits).not.toContain("notify_urls");
  });

  it("puts a label match above a keyword match", () => {
    const hits = searchSettings("events");
    // "Events" is a retention field's label; several others merely mention
    // events in passing.
    expect(hits[0].name).toBe("event_retention_days");
  });

  it("is substring, not fuzzy", () => {
    // Fuzzy matching would answer this with Vacuum, because v-a-c appears in
    // order somewhere. An answer that wrong is worse than no answer.
    expect(searchSettings("zzzz")).toEqual([]);
  });

  it("returns nothing for an empty query", () => {
    expect(searchSettings("")).toEqual([]);
    expect(searchSettings("   ")).toEqual([]);
  });

  it("carries the section label, so a hit says where it lives", () => {
    const [hit] = searchSettings("vacuum");
    expect(hit.sectionLabel).toBe("Retention");
  });
});

describe("overrideCounts", () => {
  it("counts per section", () => {
    expect(overrideCounts(["retention_days", "event_retention_days", "keep_keys"])).toEqual({
      retention: 2,
      collection: 1,
    });
  });

  it("ignores a name it does not know", () => {
    expect(overrideCounts(["not_a_setting"])).toEqual({});
  });

  it("counts nothing for nothing", () => {
    expect(overrideCounts([])).toEqual({});
  });
});

describe("the index and the screen agree", () => {
  const source = readFileSync(new URL("../routes/Settings.svelte", import.meta.url), "utf8");

  it("has an entry for every field the screen renders", () => {
    // The one way these can drift: a field added to the screen and not to the
    // index is a setting the search cannot find, which nothing else notices.
    const rendered = [...source.matchAll(/@render (?:field|choice)\(\s*"([a-z_A-Z]+)"/g)].map((m) => m[1]);
    expect(rendered.length).toBeGreaterThan(10);

    const known = new Set(SETTINGS.map((s) => s.name));
    expect([...new Set(rendered)].filter((name) => !known.has(name))).toEqual([]);
  });

  it("puts every entry in a section that exists", () => {
    const ids = new Set(SECTIONS.map((s) => s.id));
    expect(SETTINGS.filter((s) => !ids.has(s.section)).map((s) => s.name)).toEqual([]);
  });

  it("has no duplicate names", () => {
    const seen = new Set<string>();
    const dupes = SETTINGS.filter((s) => (seen.has(s.name) ? true : (seen.add(s.name), false)));
    expect(dupes.map((d) => d.name)).toEqual([]);
  });
});
