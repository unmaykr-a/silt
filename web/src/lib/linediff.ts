/**
 * Line and word diffing for the compose views.
 *
 * The server already computes a line diff for a captured file, but two places
 * need one the server has not been asked for: the side-by-side YAML comparison
 * of two snapshots, and the word-level highlight *inside* a changed line. Both
 * are presentation — nothing is stored or acted on — so they run here rather
 * than growing the API.
 *
 * The line algorithm is the same LCS the Go side uses. Word diffing then runs
 * over the pairs of lines that changed together, which is what turns "this
 * whole line is different" into "this digest is different".
 */

export type Op = "equal" | "insert" | "delete" | "replace";

export type Row = {
  op: Op;
  /** 1-based line numbers, 0 where the side has no line. */
  oldNumber: number;
  newNumber: number;
  oldText: string;
  newText: string;
};

/** Split text into lines, dropping the phantom element a trailing newline adds. */
export function lines(text: string): string[] {
  const out = text.split("\n");
  if (out.length > 0 && out[out.length - 1] === "") out.pop();
  return out;
}

/**
 * A line-level diff, with adjacent delete/insert runs paired into replaces so
 * a changed line can be shown against the line it replaced.
 */
export function diffLines(before: string[], after: string[]): Row[] {
  const table = lcs(before, after);
  const raw: Row[] = [];

  let i = 0;
  let j = 0;
  while (i < before.length && j < after.length) {
    if (before[i] === after[j]) {
      raw.push({ op: "equal", oldNumber: i + 1, newNumber: j + 1, oldText: before[i], newText: after[j] });
      i++;
      j++;
    } else if (table[i + 1][j] >= table[i][j + 1]) {
      raw.push({ op: "delete", oldNumber: i + 1, newNumber: 0, oldText: before[i], newText: "" });
      i++;
    } else {
      raw.push({ op: "insert", oldNumber: 0, newNumber: j + 1, oldText: "", newText: after[j] });
      j++;
    }
  }
  while (i < before.length) {
    raw.push({ op: "delete", oldNumber: i + 1, newNumber: 0, oldText: before[i], newText: "" });
    i++;
  }
  while (j < after.length) {
    raw.push({ op: "insert", oldNumber: 0, newNumber: j + 1, oldText: "", newText: after[j] });
    j++;
  }

  return pairRuns(raw);
}

/**
 * Pair a run of deletes with the run of inserts that follows it.
 *
 * Without this a one-character edit renders as a removed line somewhere above
 * an added line, and the eye has to do the pairing. With it the two sit on the
 * same row and the word diff can say which characters moved.
 */
function pairRuns(rows: Row[]): Row[] {
  const out: Row[] = [];
  let i = 0;
  while (i < rows.length) {
    if (rows[i].op !== "delete") {
      out.push(rows[i]);
      i++;
      continue;
    }
    let d = i;
    while (d < rows.length && rows[d].op === "delete") d++;
    let n = d;
    while (n < rows.length && rows[n].op === "insert") n++;

    const deletes = rows.slice(i, d);
    const inserts = rows.slice(d, n);
    const paired = Math.min(deletes.length, inserts.length);
    for (let k = 0; k < paired; k++) {
      out.push({
        op: "replace",
        oldNumber: deletes[k].oldNumber,
        newNumber: inserts[k].newNumber,
        oldText: deletes[k].oldText,
        newText: inserts[k].newText,
      });
    }
    for (let k = paired; k < deletes.length; k++) out.push(deletes[k]);
    for (let k = paired; k < inserts.length; k++) out.push(inserts[k]);
    i = n;
  }
  return out;
}

/**
 * Collapse long runs of unchanged lines, keeping `context` on each side.
 *
 * A compose file is mostly unchanged, and scrolling past four hundred
 * identical lines to find the one that moved is what the screenshot showed.
 */
export type Section = { kind: "rows"; rows: Row[] } | { kind: "gap"; count: number };

export function collapse(rows: Row[], context = 3): Section[] {
  const interesting = new Set<number>();
  rows.forEach((row, index) => {
    if (row.op === "equal") return;
    // Clamped before the loop rather than inside it. "Whole file" passes a
    // context wider than the file, and testing the bound per iteration meant
    // counting to that number one step at a time — which locks the tab.
    const first = Math.max(0, index - context);
    const last = Math.min(rows.length - 1, index + context);
    for (let k = first; k <= last; k++) interesting.add(k);
  });

  const out: Section[] = [];
  let i = 0;
  while (i < rows.length) {
    if (interesting.has(i)) {
      const start = i;
      while (i < rows.length && interesting.has(i)) i++;
      out.push({ kind: "rows", rows: rows.slice(start, i) });
    } else {
      const start = i;
      while (i < rows.length && !interesting.has(i)) i++;
      out.push({ kind: "gap", count: i - start });
    }
  }
  return out;
}

/** A span of a line, marked as changed or not, for intra-line highlighting. */
export type Span = { text: string; changed: boolean };

/**
 * Word-level diff of two lines.
 *
 * Splitting on word boundaries rather than characters keeps the highlight
 * legible: a changed digest reads as one marked run, not sixty-four.
 */
export function diffWords(before: string, after: string): [Span[], Span[]] {
  const a = splitWords(before);
  const b = splitWords(after);
  const table = lcs(a, b);

  const left: Span[] = [];
  const right: Span[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      push(left, a[i], false);
      push(right, b[j], false);
      i++;
      j++;
    } else if (table[i + 1][j] >= table[i][j + 1]) {
      push(left, a[i], true);
      i++;
    } else {
      push(right, b[j], true);
      j++;
    }
  }
  while (i < a.length) push(left, a[i++], true);
  while (j < b.length) push(right, b[j++], true);
  return [left, right];
}

function push(spans: Span[], text: string, changed: boolean) {
  const last = spans[spans.length - 1];
  if (last && last.changed === changed) last.text += text;
  else spans.push({ text, changed });
}

/**
 * Words, runs of whitespace, and single punctuation marks.
 *
 * `:` `.` and `/` are separators rather than word characters, so
 * `sha256:aaa` splits into three pieces and only the digest gets marked.
 * Treating them as part of the word marked the whole reference, which is the
 * same as marking the whole line.
 */
function splitWords(line: string): string[] {
  return line.match(/\s+|[A-Za-z0-9_-]+|[^\sA-Za-z0-9_-]/g) ?? [];
}

/**
 * LCS length table. table[i][j] is the length of the longest common
 * subsequence of a[i:] and b[j:].
 */
function lcs<T>(a: T[], b: T[]): number[][] {
  const table: number[][] = Array.from({ length: a.length + 1 }, () => new Array<number>(b.length + 1).fill(0));
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      table[i][j] = a[i] === b[j] ? table[i + 1][j + 1] + 1 : Math.max(table[i + 1][j], table[i][j + 1]);
    }
  }
  return table;
}
