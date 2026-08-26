/**
 * A small YAML tokenizer for display.
 *
 * Hand-written rather than pulled in, for the same reason the line diff was:
 * this highlights, it does not parse. Nothing downstream depends on it being
 * correct YAML — a mis-coloured line is a cosmetic bug, not a wrong answer —
 * and a highlighting library plus its grammar is a large dependency to carry
 * for one panel.
 *
 * It works line by line, which is what the diff needs anyway: every view that
 * shows compose files shows them as numbered lines.
 */

export type TokenKind =
  | "plain"
  | "key"
  | "string"
  | "number"
  | "boolean"
  | "null"
  | "comment"
  | "punct"
  | "anchor"
  | "tag"
  | "interp"
  | "redacted"
  | "directive";

export type Token = { kind: TokenKind; text: string };

const BOOLEANS = new Set(["true", "false", "yes", "no", "on", "off"]);
const NULLS = new Set(["null", "~"]);

/** Silt's own redaction placeholder, worth calling out in its own colour. */
const REDACTED = /^\[redacted:[0-9a-f]+\]$/;

/**
 * Tokenize one line.
 *
 * The shape of a compose line is predictable enough to do this without state:
 * indent, an optional list dash, an optional `key:`, then a scalar. Block
 * scalars (`|`, `>`) are the one thing a line-at-a-time reader cannot see, so
 * `inBlock` lets the caller carry that across lines.
 */
export function tokenizeLine(line: string, inBlock = false): Token[] {
  if (inBlock) return [{ kind: "string", text: line }];

  const out: Token[] = [];
  let rest = line;

  // Leading whitespace and any list dashes, which can nest ("- - a").
  const indent = rest.match(/^\s*/)![0];
  if (indent) out.push({ kind: "plain", text: indent });
  rest = rest.slice(indent.length);

  if (rest === "") return out;

  if (rest.startsWith("#")) {
    out.push({ kind: "comment", text: rest });
    return out;
  }
  if (rest.startsWith("---") || rest.startsWith("...") || rest.startsWith("%")) {
    out.push({ kind: "directive", text: rest });
    return out;
  }

  while (rest.startsWith("- ") || rest === "-") {
    const dash = rest === "-" ? "-" : "- ";
    out.push({ kind: "punct", text: dash });
    rest = rest.slice(dash.length);
  }
  if (rest === "") return out;

  // A key is an unquoted or quoted scalar followed by ": " or a trailing ":".
  const key = rest.match(/^("(?:[^"\\]|\\.)*"|'(?:[^']|'')*'|[^:#]+?)(:)(\s|$)/);
  if (key) {
    out.push({ kind: "key", text: key[1] });
    out.push({ kind: "punct", text: key[2] });
    if (key[3]) out.push({ kind: "plain", text: key[3] });
    rest = rest.slice(key[0].length);
    if (rest === "") return out;
  }

  out.push(...tokenizeValue(rest));
  return out;
}

/** Whether a line opens a block scalar whose body should be left alone. */
export function opensBlock(line: string): boolean {
  return /:\s*[|>][-+]?\d*\s*(#.*)?$/.test(line);
}

/** The indentation of a line, for deciding where a block scalar ends. */
export function indentOf(line: string): number {
  return line.match(/^\s*/)![0].length;
}

/** Where the current bare scalar ends. */
function bareEnd(value: string, start: number): number {
  let i = start;
  while (i < value.length) {
    const ch = value[i];
    // Flow punctuation and quotes end a bare scalar...
    if ("[]{},'\"".includes(ch)) break;
    // ...as does the start of an interpolation, which owns its own braces.
    if (ch === "$") break;
    // A `#` only ends it when it opens a comment; `http://x/#frag` does not.
    if (ch === "#" && i > start && /\s/.test(value[i - 1])) break;
    if (ch === "#" && i === start && start === 0) break;
    i++;
  }
  return i;
}

const INTERPOLATION = /^(?:\$\{[^}]*\}|\$[A-Za-z_][A-Za-z0-9_]*)/;

function tokenizeValue(value: string): Token[] {
  const out: Token[] = [];
  let i = 0;

  const flush = (kind: TokenKind, text: string) => {
    if (text) out.push({ kind, text });
  };

  while (i < value.length) {
    const ch = value[i];

    // Interpolation is matched before flow punctuation, because `${VAR}`
    // contains braces that are part of the reference rather than a flow
    // mapping. Getting this order wrong split every ${VAR} into four tokens.
    const interpolation = value.slice(i).match(INTERPOLATION);
    if (interpolation) {
      flush("interp", interpolation[0]);
      i += interpolation[0].length;
      continue;
    }

    // A comment runs to end of line, but only when it is preceded by a space
    // or starts the value: `http://x#y` is not a comment.
    if (ch === "#" && (i === 0 || /\s/.test(value[i - 1]))) {
      flush("comment", value.slice(i));
      return out;
    }

    if (ch === '"' || ch === "'") {
      const quoted = readQuoted(value, i, ch);
      const body = quoted.slice(1, -1);
      if (REDACTED.test(body)) {
        flush("redacted", quoted);
      } else {
        // Interpolation references survive redaction and are worth seeing, so
        // they keep their own colour even inside a quoted string.
        out.push(...splitInterpolation(quoted, "string"));
      }
      i += quoted.length;
      continue;
    }

    if (ch === "&" || ch === "*") {
      const m = value.slice(i).match(/^[&*][\w-]+/);
      if (m) {
        flush("anchor", m[0]);
        i += m[0].length;
        continue;
      }
    }

    if (ch === "!") {
      const m = value.slice(i).match(/^![\w!/:.-]*/);
      if (m) {
        flush("tag", m[0]);
        i += m[0].length;
        continue;
      }
    }

    if ("[]{},".includes(ch)) {
      flush("punct", ch);
      i++;
      continue;
    }

    // A bare scalar runs until a comment, a flow punctuation mark, an
    // interpolation, or the end. Always at least one character, so a lone `$`
    // that opens nothing cannot stall the loop.
    const text = value.slice(i, Math.max(bareEnd(value, i), i + 1));
    const trimmed = text.trim();
    const lower = trimmed.toLowerCase();

    if (REDACTED.test(trimmed)) {
      out.push(...padded(text, "redacted"));
    } else if (NULLS.has(lower)) {
      out.push(...padded(text, "null"));
    } else if (BOOLEANS.has(lower)) {
      out.push(...padded(text, "boolean"));
    } else if (trimmed !== "" && /^-?(\d+\.?\d*|\.\d+)([eE][-+]?\d+)?$/.test(trimmed)) {
      out.push(...padded(text, "number"));
    } else {
      out.push(...padded(text, "plain"));
    }
    i += text.length;
  }

  return out;
}

/** Keep surrounding whitespace uncoloured so a value's own colour is its own. */
function padded(text: string, kind: TokenKind): Token[] {
  const lead = text.match(/^\s*/)![0];
  const tail = text.match(/\s*$/)![0];
  const body = text.slice(lead.length, text.length - tail.length);
  const out: Token[] = [];
  if (lead) out.push({ kind: "plain", text: lead });
  if (body) out.push({ kind, text: body });
  if (tail) out.push({ kind: "plain", text: tail });
  return out;
}

/** Split `${VAR}` and `$VAR` out of a scalar so references stand out. */
function splitInterpolation(text: string, base: TokenKind): Token[] {
  const out: Token[] = [];
  const pattern = /\$\{[^}]*\}|\$[A-Za-z_][A-Za-z0-9_]*/g;
  let last = 0;
  for (const match of text.matchAll(pattern)) {
    const at = match.index!;
    if (at > last) out.push(...padded(text.slice(last, at), base));
    out.push({ kind: "interp", text: match[0] });
    last = at + match[0].length;
  }
  if (last < text.length) out.push(...padded(text.slice(last), base));
  return out;
}

function readQuoted(value: string, start: number, quote: string): string {
  let i = start + 1;
  while (i < value.length) {
    if (quote === '"' && value[i] === "\\") {
      i += 2;
      continue;
    }
    if (value[i] === quote) {
      // '' is an escaped quote inside a single-quoted scalar.
      if (quote === "'" && value[i + 1] === "'") {
        i += 2;
        continue;
      }
      return value.slice(start, i + 1);
    }
    i++;
  }
  return value.slice(start);
}

/**
 * Tokenize a whole document, carrying block-scalar state across lines.
 */
export function tokenizeDocument(lines: string[]): Token[][] {
  const out: Token[][] = [];
  let blockIndent = -1;
  for (const line of lines) {
    const inBlock = blockIndent >= 0 && (line.trim() === "" || indentOf(line) > blockIndent);
    if (blockIndent >= 0 && !inBlock) blockIndent = -1;
    out.push(tokenizeLine(line, inBlock));
    if (!inBlock && opensBlock(line)) blockIndent = indentOf(line);
  }
  return out;
}

/** Tailwind classes per token kind. Both themes, via CSS variables where the
 *  contrast differs enough to matter. */
export const TOKEN_CLASS: Record<TokenKind, string> = {
  plain: "",
  key: "text-sky-700 dark:text-sky-300",
  string: "text-emerald-700 dark:text-emerald-300",
  number: "text-amber-700 dark:text-amber-300",
  boolean: "text-fuchsia-700 dark:text-fuchsia-300",
  null: "text-fuchsia-700 dark:text-fuchsia-300",
  comment: "text-zinc-500 italic dark:text-zinc-500",
  punct: "text-zinc-500 dark:text-zinc-400",
  anchor: "text-violet-700 dark:text-violet-300",
  tag: "text-violet-700 dark:text-violet-300",
  interp: "text-orange-700 dark:text-orange-300",
  redacted: "text-rose-700/90 dark:text-rose-400/90",
  directive: "text-zinc-500 dark:text-zinc-400",
};
