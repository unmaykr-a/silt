import { describe, expect, it } from "vitest";
import { collapse, diffLines, diffWords, lines } from "./linediff";

describe("lines", () => {
  it("drops the phantom element a trailing newline adds", () => {
    expect(lines("a\nb\n")).toEqual(["a", "b"]);
    expect(lines("a\nb")).toEqual(["a", "b"]);
  });
});

describe("diffLines", () => {
  it("pairs a delete with the insert that replaced it", () => {
    const rows = diffLines(["a", "b", "c"], ["a", "B", "c"]);
    expect(rows.map((r) => r.op)).toEqual(["equal", "replace", "equal"]);
    expect([rows[1].oldText, rows[1].newText]).toEqual(["b", "B"]);
    expect([rows[1].oldNumber, rows[1].newNumber]).toEqual([2, 2]);
  });

  it("leaves an unmatched insert as an insert", () => {
    const rows = diffLines(["a"], ["a", "b"]);
    expect(rows.map((r) => r.op)).toEqual(["equal", "insert"]);
    expect(rows[1].oldNumber).toBe(0);
  });

  it("leaves an unmatched delete as a delete", () => {
    const rows = diffLines(["a", "b"], ["a"]);
    expect(rows.map((r) => r.op)).toEqual(["equal", "delete"]);
    expect(rows[1].newNumber).toBe(0);
  });

  // A run of three removed lines against two added ones is two replacements
  // and one removal, not five unpaired rows.
  it("pairs uneven runs and leaves the remainder", () => {
    const rows = diffLines(["a", "x", "y", "z", "b"], ["a", "X", "Y", "b"]);
    expect(rows.map((r) => r.op)).toEqual(["equal", "replace", "replace", "delete", "equal"]);
  });

  it("reports nothing for identical input", () => {
    expect(diffLines(["a", "b"], ["a", "b"]).every((r) => r.op === "equal")).toBe(true);
  });

  it("handles an empty side", () => {
    expect(diffLines([], ["a"]).map((r) => r.op)).toEqual(["insert"]);
    expect(diffLines(["a"], []).map((r) => r.op)).toEqual(["delete"]);
  });
});

describe("diffWords", () => {
  // The point of a word diff: a changed image digest should mark the digest,
  // not the whole line it sits on.
  it("marks only the part that moved", () => {
    const [left, right] = diffWords("image: sha256:aaa", "image: sha256:bbb");
    expect(left.filter((s) => s.changed).map((s) => s.text)).toEqual(["aaa"]);
    expect(right.filter((s) => s.changed).map((s) => s.text)).toEqual(["bbb"]);
  });

  it("splits on the separators inside a reference", () => {
    const [, right] = diffWords("image: ghcr.io/a/silt:1.0", "image: ghcr.io/a/silt:2.0");
    expect(right.filter((s) => s.changed).map((s) => s.text).join("")).toBe("2");
  });

  // Whatever it marks, the spans must reassemble into the original lines, or
  // the rendered line would differ from the captured one.
  it("preserves both lines exactly", () => {
    const before = "  PASSWORD: '[redacted:aaaaaaaaaaaa]'";
    const after = "  PASSWORD: '[redacted:bbbbbbbbbbbb]'";
    const [left, right] = diffWords(before, after);
    expect(left.map((s) => s.text).join("")).toBe(before);
    expect(right.map((s) => s.text).join("")).toBe(after);
  });

  it("marks everything when nothing is shared", () => {
    const [left, right] = diffWords("aaa", "bbb");
    expect(left.every((s) => s.changed)).toBe(true);
    expect(right.every((s) => s.changed)).toBe(true);
  });
});

describe("collapse", () => {
  const long = Array.from({ length: 40 }, (_, i) => `line ${i}`);

  it("keeps context around a change and folds the rest", () => {
    const changed = [...long];
    changed[20] = "line 20 changed";
    const sections = collapse(diffLines(long, changed), 3);
    expect(sections.map((s) => s.kind)).toEqual(["gap", "rows", "gap"]);
    expect(sections[1].kind === "rows" && sections[1].rows.length).toBe(7);
  });

  // Folding is presentation: every row must still be accounted for, or the
  // line numbers on either side of a gap would not add up.
  it("loses nothing", () => {
    const changed = [...long];
    changed[5] = "changed";
    changed[30] = "also changed";
    const rows = diffLines(long, changed);
    const sections = collapse(rows, 3);
    const total = sections.reduce((n, s) => n + (s.kind === "gap" ? s.count : s.rows.length), 0);
    expect(total).toBe(rows.length);
  });

  it("folds an identical file into one gap", () => {
    expect(collapse(diffLines(long, long), 3).map((s) => s.kind)).toEqual(["gap"]);
  });

  it("shows everything when asked for unlimited context", () => {
    const changed = [...long];
    changed[20] = "changed";
    const sections = collapse(diffLines(long, changed), Number.MAX_SAFE_INTEGER);
    expect(sections.map((s) => s.kind)).toEqual(["rows"]);
  });
});
