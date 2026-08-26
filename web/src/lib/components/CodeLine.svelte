<script lang="ts">
  import { tokenizeLine, TOKEN_CLASS } from "$lib/yaml";
  import { diffWords, type Span } from "$lib/linediff";

  // One highlighted line. Syntax colour comes from the tokenizer; the changed
  // spans, when a counterpart line is given, are painted on top of it.
  //
  // The two are layered rather than merged because they answer different
  // questions — "what kind of thing is this" and "what moved" — and a reader
  // needs both at once. Syntax is the text colour, change is the background.
  let {
    text,
    against,
    side = "new",
    highlight = true,
    inBlock = false,
  }: {
    text: string;
    /** The line this one replaced (or was replaced by), for a word diff. */
    against?: string;
    side?: "old" | "new";
    highlight?: boolean;
    inBlock?: boolean;
  } = $props();

  const changed = $derived.by<Span[] | null>(() => {
    if (against === undefined) return null;
    const [left, right] = diffWords(side === "old" ? text : against, side === "old" ? against : text);
    return side === "old" ? left : right;
  });

  // Token boundaries and word-diff boundaries do not line up, so the two are
  // merged into one list of fragments that each carry a colour and a flag.
  type Fragment = { text: string; cls: string; changed: boolean };

  const fragments = $derived.by<Fragment[]>(() => {
    const tokens = highlight ? tokenizeLine(text, inBlock) : [{ kind: "plain" as const, text }];
    const spans = changed;
    const out: Fragment[] = [];

    let at = 0;
    for (const token of tokens) {
      const cls = highlight ? TOKEN_CLASS[token.kind] : "";
      if (!spans) {
        out.push({ text: token.text, cls, changed: false });
        at += token.text.length;
        continue;
      }
      // Walk the change spans that overlap this token.
      let offset = 0;
      while (offset < token.text.length) {
        const absolute = at + offset;
        const span = spanAt(spans, absolute);
        const take = Math.min(token.text.length - offset, span.end - absolute);
        out.push({ text: token.text.slice(offset, offset + take), cls, changed: span.changed });
        offset += take;
      }
      at += token.text.length;
    }
    return out;
  });

  function spanAt(spans: Span[], index: number): { changed: boolean; end: number } {
    let start = 0;
    for (const span of spans) {
      const end = start + span.text.length;
      if (index < end) return { changed: span.changed, end };
      start = end;
    }
    return { changed: false, end: Number.MAX_SAFE_INTEGER };
  }

  const markClass = $derived(
    side === "old"
      ? "rounded-[2px] bg-red-500/25 dark:bg-red-500/30"
      : "rounded-[2px] bg-emerald-500/25 dark:bg-emerald-500/30",
  );
</script><!--
  No whitespace between the spans below: this renders inside `whitespace-pre`,
  and a newline from the template would become a space in the output.
--><span class="whitespace-pre">{#each fragments as fragment, i (i)}<span
      class="{fragment.cls} {fragment.changed ? markClass : ''}">{fragment.text}</span>{/each}</span>
