import { describe, expect, it } from "vitest";
import { tokenizeLine, tokenizeDocument, TOKEN_CLASS, type TokenKind } from "./yaml";

/** A compact rendering of a line's tokens, for readable assertions. */
const kinds = (line: string) => tokenizeLine(line).map((t) => `${t.kind}:${t.text}`);

describe("tokenizeLine", () => {
  it("separates a key from its quoted value", () => {
    expect(kinds('  image: "ghcr.io/x:1"')).toEqual([
      "plain:  ",
      "key:image",
      "punct::",
      "plain: ",
      'string:"ghcr.io/x:1"',
    ]);
  });

  it("marks list items", () => {
    expect(kinds("      - /silt")).toEqual(["plain:      ", "punct:- ", "plain:/silt"]);
  });

  it("recognises numbers and booleans", () => {
    expect(kinds("  container-number: 1").at(-1)).toBe("number:1");
    expect(kinds("  oneoff: false").at(-1)).toBe("boolean:false");
    expect(kinds("  extra: null").at(-1)).toBe("null:null");
  });

  it("colours Silt's redaction placeholder as its own thing", () => {
    expect(kinds("  PASSWORD: '[redacted:5c9712aa26be]'").at(-1)).toBe(
      "redacted:'[redacted:5c9712aa26be]'",
    );
  });

  // Interpolation survives redaction — seeing which variable a service reads is
  // itself worth noticing — so it has to survive the tokenizer too.
  it("keeps an interpolation reference whole", () => {
    expect(kinds("  PUID: ${PUID:-1000}").at(-1)).toBe("interp:${PUID:-1000}");
    expect(kinds("  PUID: $PUID").at(-1)).toBe("interp:$PUID");
  });

  it("does not mistake a fragment for a comment", () => {
    expect(kinds("  source: https://example.com/#frag").at(-1)).toBe(
      "plain:https://example.com/#frag",
    );
  });

  it("does treat a spaced hash as a comment", () => {
    expect(kinds("key: v # why").at(-1)).toBe("comment:# why");
    expect(kinds("# whole line")).toEqual(["comment:# whole line"]);
  });

  it("handles anchors, empty values and flow punctuation", () => {
    expect(kinds("  <<: *common").at(-1)).toBe("anchor:*common");
    expect(kinds("  command:")).toEqual(["plain:  ", "key:command", "punct::"]);
    expect(kinds("  command: []").slice(-2)).toEqual(["punct:[", "punct:]"]);
  });

  // The property that matters most: whatever the tokenizer decides, the tokens
  // must concatenate back to the original line. Anything else means the file
  // Silt shows is not the file Silt captured.
  it.each([
    '  image: "ghcr.io/x:1"',
    "      - /silt",
    "key: v # why",
    "  PASSWORD: '[redacted:5c9712aa26be]'",
    "  source: https://example.com/#frag",
    '    org.opencontainers.image.created: "2026-08-26T07:33:59.038Z"',
    "  PUID: ${PUID:-1000}",
    "  price: 5$",
    "  mixed: ${A}-${B} and $C",
    "",
    "   ",
    "- - nested",
    "  weird: 'it''s quoted'",
    "  unterminated: \"oops",
    "  braces: {a: 1, b: 2}",
  ])("round-trips %j", (line) => {
    expect(tokenizeLine(line).map((t) => t.text).join("")).toBe(line);
  });
});

describe("tokenizeDocument", () => {
  // A shell script inside a block scalar is not YAML, and colouring it as YAML
  // makes it harder to read rather than easier.
  const doc = ["cmd: |", "  echo hi: there", "  # not a comment", "next: 1"];

  it("leaves a block scalar body alone", () => {
    const tokens = tokenizeDocument(doc);
    expect(tokens[1].map((t) => t.kind)).toEqual(["string"]);
    expect(tokens[2].map((t) => t.kind)).toEqual(["string"]);
  });

  it("ends the block at the first dedent", () => {
    expect(tokenizeDocument(doc)[3].map((t) => t.kind)).toEqual(["key", "punct", "plain", "number"]);
  });

  it("round-trips a whole document", () => {
    const out = tokenizeDocument(doc).map((line) => line.map((t) => t.text).join(""));
    expect(out).toEqual(doc);
  });
});

it("has a class for every token kind the tokenizer can emit", () => {
  const emitted = new Set<TokenKind>();
  for (const line of tokenizeDocument([
    "# c",
    "---",
    "a: 1",
    'b: "s"',
    "c: true",
    "d: null",
    "e: ${V}",
    "f: '[redacted:abc123]'",
    "g: *anchor",
    "h: !!str x",
    "i: [1]",
  ])) {
    for (const token of line) emitted.add(token.kind);
  }
  for (const kind of emitted) {
    expect(TOKEN_CLASS, `missing class for ${kind}`).toHaveProperty(kind);
  }
});
