import { describe, it, expect } from "vitest";
import { buildPatch, emptyDraft, toDraft, list, multiline, type Effective } from "./patch";

// buildPatch decides what a save actually sends, and until this file existed
// nothing checked it. Being wrong here is silent: a patch that restates a field
// nobody touched writes an override for it, which detaches that field from the
// environment it was tracking — visible only on the next container recreate,
// when the environment change everyone expected does not take.

const effective: Effective = {
  snapshot_interval_ms: 300_000,
  retention_days: 365,
  unchanged_retention_days: 7,
  event_retention_days: 90,
  audit_retention_days: 730,
  retention_interval_ms: 3_600_000,
  vacuum_interval_ms: 0,
  keep_keys: ["PUID", "TZ"],
  base_url: "https://silt.example.lan",
  log_level: "info",
  notify_targets: ["ntfy://***"],
  notify_on: ["image_id", "volumes"],
  notify_min_severity: "medium",
  ingest_configured: true,
  ingest_rate_per_minute: 60,
};

const none = { notifyUrls: "", ingestToken: "" };

describe("buildPatch", () => {
  it("sends nothing when nothing changed", () => {
    expect(buildPatch(toDraft(effective), effective, none)).toEqual({});
  });

  it("sends only the field that changed", () => {
    const draft = { ...toDraft(effective), retention_days: 30 };
    expect(buildPatch(draft, effective, none)).toEqual({ retention_days: 30 });
  });

  it("treats a numeric string as its number", () => {
    // An <input type="number"> hands back a string on some paths. Comparing it
    // raw would make "365" !== 365 and every save permanently dirty.
    const draft = { ...toDraft(effective), retention_days: "365" as unknown as number };
    expect(buildPatch(draft, effective, none)).toEqual({});
  });

  it("normalises a list before comparing it", () => {
    // Retyping the same keys with different spacing is not a change.
    const draft = { ...toDraft(effective), keep_keys: "PUID,   TZ  " };
    expect(buildPatch(draft, effective, none)).toEqual({});
  });

  it("sends a changed list as an array", () => {
    const draft = { ...toDraft(effective), keep_keys: "PUID, TZ, APP_*" };
    expect(buildPatch(draft, effective, none)).toEqual({ keep_keys: ["PUID", "TZ", "APP_*"] });
  });

  it("sends an emptied list as an empty array rather than omitting it", () => {
    // Omitting it would mean "leave it alone", and the person just cleared the
    // box. These are different intentions and the patch has to carry the one
    // they had.
    const draft = { ...toDraft(effective), keep_keys: "" };
    expect(buildPatch(draft, effective, none)).toEqual({ keep_keys: [] });
  });

  it("only sends a secret when it was typed into", () => {
    const draft = toDraft(effective);
    expect(buildPatch(draft, effective, none)).toEqual({});
    expect(buildPatch(draft, effective, { notifyUrls: "  ", ingestToken: "\n" })).toEqual({});
    expect(
      buildPatch(draft, effective, { notifyUrls: "ntfy://a\ndiscord://b", ingestToken: " tok " }),
    ).toEqual({ notify_urls: ["ntfy://a", "discord://b"], ingest_token: "tok" });
  });

  it("sends several changes together", () => {
    const draft = { ...toDraft(effective), retention_days: 30, log_level: "debug", base_url: "" };
    expect(buildPatch(draft, effective, none)).toEqual({
      retention_days: 30,
      log_level: "debug",
      base_url: "",
    });
  });

  it("sends zero, which means keep forever rather than unset", () => {
    const draft = { ...toDraft(effective), retention_days: 0 };
    expect(buildPatch(draft, effective, none)).toEqual({ retention_days: 0 });
  });

  it("does not confuse an empty draft with a matching one", () => {
    // emptyDraft is what renders before anything loads. It happens to hold the
    // documented defaults, so against an install running those it is clean —
    // and against any other install every field it names is a change.
    expect(buildPatch(emptyDraft(), effective, none)).toEqual({
      keep_keys: [],
      base_url: "",
      notify_on: [],
    });
  });
});

describe("list and multiline", () => {
  it("drops the empties a trailing comma leaves", () => {
    expect(list("a, b,")).toEqual(["a", "b"]);
    expect(list("")).toEqual([]);
    expect(list("   ")).toEqual([]);
  });

  it("splits a textarea on newlines as well as commas", () => {
    expect(multiline("a\nb, c\n\n")).toEqual(["a", "b", "c"]);
  });
});
