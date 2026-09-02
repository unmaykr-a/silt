import { describe, expect, it } from "vitest";
import { diffToMarkdown, yamlToUnifiedDiff } from "./diffexport";
import { diffLines, lines } from "./linediff";
import type { Diff } from "./api/client";

const base = {
  from: { id: 131, project_id: 1, taken_at: 1_700_000_000_000 },
  to: { id: 143, project_id: 1, taken_at: 1_700_003_600_000 },
} as unknown as Diff;

function withChanges(changes: Diff["changes"], summary: Record<string, number> = {}): Diff {
  return { ...base, changes, summary } as Diff;
}

describe("diffToMarkdown", () => {
  it("groups by service and then by kind, the way the screen does", () => {
    const out = diffToMarkdown(
      withChanges([
        { service: "radarr", kind: "image_id", severity: "medium", path: "image", before: "a", after: "b" },
        { service: "radarr", kind: "image_id", severity: "medium", path: "digest", before: "c", after: "d" },
        { service: "sonarr", kind: "env", severity: "low", path: "env.TZ", before: "", after: "UTC" },
      ] as Diff["changes"]),
      "media",
    );
    expect(out).toContain("## media: what changed");
    expect(out).toContain("### radarr");
    expect(out).toContain("### sonarr");
    expect(out.indexOf("### radarr")).toBeLessThan(out.indexOf("### sonarr"));
    // One heading per kind, not one per change.
    expect(out.match(/\*\*image_id\*\*/g)).toHaveLength(1);
  });

  it("renders each shape of change", () => {
    const out = diffToMarkdown(
      withChanges([
        { service: "s", kind: "image_id", severity: "medium", path: "changed", before: "a", after: "b" },
        { service: "s", kind: "volumes", severity: "high", path: "added", before: "", after: "/data" },
        { service: "s", kind: "volumes", severity: "high", path: "removed", before: "/old", after: "" },
      ] as Diff["changes"]),
    );
    expect(out).toContain("`changed`: `a` → `b`");
    expect(out).toContain("`added`: added `/data`");
    expect(out).toContain("`removed`: removed `/old`");
  });

  it("names both snapshots so the export says what it compared", () => {
    const out = diffToMarkdown(withChanges([]));
    expect(out).toContain("#131");
    expect(out).toContain("#143");
  });

  it("says so when nothing changed", () => {
    expect(diffToMarkdown(withChanges([]))).toContain("identical");
  });

  // The export must not become a way around redaction. A redacted value is a
  // placeholder by the time it reaches the client, and it leaves as one.
  it("exports a redacted value as its placeholder", () => {
    const out = diffToMarkdown(
      withChanges([
        {
          service: "s",
          kind: "env",
          severity: "medium",
          path: "env.SMTP_PASSWORD",
          before: "[redacted:aaaaaaaaaaaa]",
          after: "[redacted:bbbbbbbbbbbb]",
        },
      ] as Diff["changes"]),
    );
    expect(out).toContain("[redacted:aaaaaaaaaaaa]");
    expect(out).toContain("[redacted:bbbbbbbbbbbb]");
    expect(out).toContain("Silt never stored them");
  });
});

describe("yamlToUnifiedDiff", () => {
  const before = "services:\n  a:\n    image: x:1\n";
  const after = "services:\n  a:\n    image: x:2\n";

  it("produces the shape patch expects", () => {
    const out = yamlToUnifiedDiff("a/s-131.yaml", "b/s-143.yaml", diffLines(lines(before), lines(after)));
    const rows = out.split("\n");
    expect(rows[0]).toBe("--- a/s-131.yaml");
    expect(rows[1]).toBe("+++ b/s-143.yaml");
    expect(rows[2]).toMatch(/^@@ -\d+,\d+ \+\d+,\d+ @@$/);
    expect(rows).toContain(" services:");
    expect(rows).toContain("-    image: x:1");
    expect(rows).toContain("+    image: x:2");
    expect(out.endsWith("\n")).toBe(true);
  });

  it("emits nothing for two identical documents", () => {
    // A hunkless diff is the empty string, not a header over unchanged lines:
    // patch would have nothing to apply and a reader nothing to read.
    expect(yamlToUnifiedDiff("a", "b", diffLines(lines(before), lines(before)))).toBe("");
  });

  it("renders an insert and a delete on their own", () => {
    const out = yamlToUnifiedDiff("a", "b", diffLines(["a"], ["a", "b"]));
    expect(out).toContain("+b");
    const removed = yamlToUnifiedDiff("a", "b", diffLines(["a", "b"], ["a"]));
    expect(removed).toContain("-b");
  });

  it("counts hunk lines per side", () => {
    // The changed line is the document's last, so with one line of context the
    // hunk is the line before it and the change: two lines on each side,
    // starting at line 2.
    const out = yamlToUnifiedDiff("a", "b", diffLines(lines(before), lines(after)), 1);
    expect(out.split("\n")[2]).toBe("@@ -2,2 +2,2 @@");
  });

  it("keeps distant edits in separate hunks", () => {
    const l = (n: number) => Array.from({ length: n }, (_, i) => `line${i}`);
    const from = l(30);
    const to = [...from];
    to[2] = "changed-early";
    to[25] = "changed-late";
    const out = yamlToUnifiedDiff("a", "b", diffLines(from, to));
    expect(out.split("\n").filter((r) => r.startsWith("@@"))).toHaveLength(2);
  });

  it("does not carry the whole document when one line changed", () => {
    const l = Array.from({ length: 200 }, (_, i) => `line${i}`);
    const to = [...l];
    to[100] = "changed";
    const out = yamlToUnifiedDiff("a", "b", diffLines(l, to));
    // Two headers, one hunk header, and at most 3+1+1+3 body lines.
    expect(out.trimEnd().split("\n").length).toBeLessThan(15);
  });
});
