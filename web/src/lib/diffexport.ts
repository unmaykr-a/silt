/**
 * Rendering a diff as text you can paste somewhere else.
 *
 * Silt answers "what changed" on screen, and then the answer has to travel —
 * into an issue, a message to whoever also runs the host, a note to yourself.
 * Screenshotting a diff loses the text; this keeps it.
 *
 * Markdown rather than JSON: the destinations are places people read.
 */
import type { Change, Diff } from "./api/client";

/**
 * Timestamps in an export are ISO 8601, not the reader's preferred format.
 *
 * The viewer's dd/mm/yyyy is right on screen and wrong in a file that travels:
 * whoever opens the issue may read dates the other way round, and an exported
 * artefact should not be ambiguous about when it happened.
 */
export function stamp(ts: number): string {
  return new Date(ts).toISOString().replace(".000Z", "Z");
}

/** Group changes the way the screen does: by service, then by kind. */
function grouped(changes: Change[]): Map<string, Map<string, Change[]>> {
  const out = new Map<string, Map<string, Change[]>>();
  for (const change of changes) {
    const service = change.service || "project";
    if (!out.has(service)) out.set(service, new Map());
    const byKind = out.get(service)!;
    if (!byKind.has(change.kind)) byKind.set(change.kind, []);
    byKind.get(change.kind)!.push(change);
  }
  return out;
}

/**
 * A structured diff as Markdown.
 *
 * Values pass through exactly as the API returned them, which means a redacted
 * one is exported as its placeholder. That is the point: the export cannot leak
 * what Silt never stored.
 */
export function diffToMarkdown(diff: Diff, projectName?: string): string {
  const lines: string[] = [];
  const title = projectName ? `${projectName}: what changed` : "What changed";
  lines.push(`## ${title}`, "");
  lines.push(
    `Snapshot #${diff.from.id} (${stamp(diff.from.taken_at)}) → ` +
      `#${diff.to.id} (${stamp(diff.to.taken_at)})`,
    "",
  );

  const summary = Object.entries(diff.summary ?? {});
  if (summary.length > 0) {
    lines.push(summary.map(([kind, count]) => `${kind}: ${count}`).join(" · "), "");
  }

  if ((diff.changes ?? []).length === 0) {
    lines.push("_These two snapshots are identical._", "");
    return lines.join("\n");
  }

  for (const [service, byKind] of grouped(diff.changes)) {
    lines.push(`### ${service}`, "");
    for (const [kind, changes] of byKind) {
      lines.push(`**${kind}** (${changes[0].severity})`, "");
      for (const change of changes) {
        const before = change.before ?? "";
        const after = change.after ?? "";
        if (before && after) {
          lines.push(`- \`${change.path}\`: \`${before}\` → \`${after}\``);
        } else if (after) {
          lines.push(`- \`${change.path}\`: added \`${after}\``);
        } else if (before) {
          lines.push(`- \`${change.path}\`: removed \`${before}\``);
        } else {
          lines.push(`- \`${change.path}\``);
        }
      }
      lines.push("");
    }
  }

  lines.push("_Redacted values appear as their placeholder; Silt never stored them._");
  return lines.join("\n");
}

/**
 * A unified text diff of two YAML documents, in the shape patch(1) expects.
 *
 * The rows come from the same line diff the screen renders, so what you paste
 * is what you were looking at — but the file has to survive leaving the screen.
 * A `.diff` that `git apply` rejects is a worse artefact than plain text
 * pretending to be nothing, so this emits real `@@` hunks with three lines of
 * context rather than a full-context dump with the whole document in it.
 */
export function yamlToUnifiedDiff(
  fromLabel: string,
  toLabel: string,
  rows: { op: string; oldText: string; newText: string }[],
  context = 3,
): string {
  type Op = { sign: " " | "-" | "+"; text: string };
  const ops: Op[] = [];
  for (const row of rows) {
    switch (row.op) {
      case "equal":
        ops.push({ sign: " ", text: row.oldText });
        break;
      case "delete":
        ops.push({ sign: "-", text: row.oldText });
        break;
      case "insert":
        ops.push({ sign: "+", text: row.newText });
        break;
      case "replace":
        ops.push({ sign: "-", text: row.oldText }, { sign: "+", text: row.newText });
        break;
    }
  }

  // Line numbers each op occupies in the old and new document. A hunk header
  // counts lines, not ops, and the two sides count different subsets.
  const oldNo: number[] = [];
  const newNo: number[] = [];
  let o = 0;
  let n = 0;
  for (const op of ops) {
    if (op.sign !== "+") o += 1;
    if (op.sign !== "-") n += 1;
    oldNo.push(o);
    newNo.push(n);
  }

  const changed = ops.map((op) => op.sign !== " ");
  if (!changed.some(Boolean)) return "";

  // Group changed lines that are within 2*context of each other into one hunk,
  // which is what keeps a run of small edits from becoming a hunk apiece.
  const hunks: { start: number; end: number }[] = [];
  for (let i = 0; i < ops.length; i++) {
    if (!changed[i]) continue;
    const from = Math.max(0, i - context);
    const to = Math.min(ops.length - 1, i + context);
    const last = hunks[hunks.length - 1];
    if (last && from <= last.end + 1) last.end = Math.max(last.end, to);
    else hunks.push({ start: from, end: to });
  }

  const out = [`--- ${fromLabel}`, `+++ ${toLabel}`];
  for (const hunk of hunks) {
    let oldCount = 0;
    let newCount = 0;
    for (let i = hunk.start; i <= hunk.end; i++) {
      if (ops[i].sign !== "+") oldCount += 1;
      if (ops[i].sign !== "-") newCount += 1;
    }
    // A hunk that adds to an empty side starts at 0, per the unified format.
    const oldStart = oldCount === 0 ? 0 : oldNo[hunk.start] - (ops[hunk.start].sign === "+" ? 0 : 1) + 1;
    const newStart = newCount === 0 ? 0 : newNo[hunk.start] - (ops[hunk.start].sign === "-" ? 0 : 1) + 1;
    out.push(`@@ -${oldStart},${oldCount} +${newStart},${newCount} @@`);
    for (let i = hunk.start; i <= hunk.end; i++) out.push(`${ops[i].sign}${ops[i].text}`);
  }
  // Trailing newline: a text file without one is what makes `patch` complain
  // about the last line.
  return out.join("\n") + "\n";
}

/**
 * Put text on the clipboard, falling back to a hidden textarea.
 *
 * navigator.clipboard needs a secure context, and Silt is commonly reached over
 * plain HTTP on a LAN — where the modern API simply is not there.
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Fall through: a permissions policy can refuse even in a secure context.
  }
  try {
    const area = document.createElement("textarea");
    area.value = text;
    area.setAttribute("readonly", "");
    area.style.position = "fixed";
    area.style.opacity = "0";
    document.body.appendChild(area);
    area.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(area);
    return ok;
  } catch {
    return false;
  }
}

/** Offer text as a file download. */
export function downloadText(filename: string, text: string) {
  const blob = new Blob([text], { type: "text/plain;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  document.body.removeChild(anchor);
  // Revoked on the next tick: revoking synchronously can race the download in
  // some browsers.
  setTimeout(() => URL.revokeObjectURL(url), 0);
}
