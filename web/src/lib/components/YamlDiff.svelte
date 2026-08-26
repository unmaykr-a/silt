<script lang="ts">
  import CodeLine from "./CodeLine.svelte";
  import { lines, diffLines, collapse, type Row } from "$lib/linediff";

  // The side-by-side comparison of two compose documents.
  //
  // Two full documents next to each other with no marking is what the previous
  // version showed, and finding the changed line in four hundred identical
  // ones is a job for the reader that the machine should be doing. Unchanged
  // runs collapse, changed lines are tinted, and the changed words inside them
  // are marked.
  let {
    before,
    after,
    beforeLabel = "before",
    afterLabel = "after",
  }: { before: string; after: string; beforeLabel?: string; afterLabel?: string } = $props();

  let context = $state(3);
  let split = $state(true);

  const rows = $derived(diffLines(lines(before), lines(after)));
  const sections = $derived(collapse(rows, context));
  const counts = $derived.by(() => {
    let added = 0;
    let removed = 0;
    let changed = 0;
    for (const row of rows) {
      if (row.op === "insert") added++;
      else if (row.op === "delete") removed++;
      else if (row.op === "replace") changed++;
    }
    return { added, removed, changed };
  });

  function rowBg(row: Row, side: "old" | "new"): string {
    if (row.op === "equal") return "";
    if (row.op === "replace") {
      return side === "old" ? "bg-red-500/[0.07]" : "bg-emerald-500/[0.07]";
    }
    if (row.op === "delete") return side === "old" ? "bg-red-500/[0.12]" : "opacity-40";
    return side === "new" ? "bg-emerald-500/[0.12]" : "opacity-40";
  }

  function has(row: Row, side: "old" | "new"): boolean {
    return side === "old" ? row.op !== "insert" : row.op !== "delete";
  }

  // A replaced line has a counterpart to word-diff against; an added or
  // removed one does not, and marking every character of it would be noise.
  function counterpart(row: Row, side: "old" | "new"): string | undefined {
    if (row.op !== "replace") return undefined;
    return side === "old" ? row.newText : row.oldText;
  }
</script>

<div class="space-y-2">
  <div class="flex flex-wrap items-center gap-x-4 gap-y-2 text-xs">
    <span class="flex items-center gap-3 font-mono">
      <span class="text-emerald-600 dark:text-emerald-400">+{counts.added}</span>
      <span class="text-red-600 dark:text-red-400">−{counts.removed}</span>
      <span class="text-amber-600 dark:text-amber-400">~{counts.changed}</span>
    </span>

    <span class="ml-auto flex items-center gap-2">
      <label class="flex items-center gap-1.5 text-muted-foreground">
        <input type="checkbox" bind:checked={split} class="accent-emerald-500" />
        Side by side
      </label>
      <select
        bind:value={context}
        class="rounded border border-border bg-background px-1.5 py-1 text-[11px] text-foreground"
        aria-label="Lines of context"
      >
        <option value={0}>no context</option>
        <option value={3}>3 lines</option>
        <option value={10}>10 lines</option>
        <option value={Infinity}>whole file</option>
      </select>
    </span>
  </div>

  {#if counts.added + counts.removed + counts.changed === 0}
    <p class="rounded-md border border-border bg-card px-4 py-3 text-sm text-muted-foreground">
      These two are identical.
    </p>
  {:else}
    <div class="overflow-x-auto rounded-md border border-border bg-card">
      <div class="sticky top-0 z-10 flex border-b border-border bg-card/95 text-[11px] text-muted-foreground backdrop-blur-sm">
        {#if split}
          <span class="flex-1 border-r border-border px-3 py-1.5">{beforeLabel}</span>
          <span class="flex-1 px-3 py-1.5">{afterLabel}</span>
        {:else}
          <span class="px-3 py-1.5">{beforeLabel} → {afterLabel}</span>
        {/if}
      </div>

      {#each sections as section, s (s)}
        {#if section.kind === "gap"}
          <div class="border-y border-border bg-secondary/25 px-3 py-1 font-mono text-[11px] text-muted-foreground">
            ⋯ {section.count} unchanged {section.count === 1 ? "line" : "lines"}
          </div>
        {:else if split}
          {#each section.rows as row, i (i)}
            <div class="flex text-xs leading-[1.6]">
              <div class="flex min-w-0 flex-1 border-r border-border {rowBg(row, 'old')}">
                <span class="w-11 shrink-0 select-none px-2 text-right font-mono text-muted-foreground/40">
                  {row.oldNumber || ""}
                </span>
                <span class="min-w-0 flex-1 pr-3 font-mono">
                  {#if has(row, "old")}
                    <CodeLine text={row.oldText} against={counterpart(row, "old")} side="old" />
                  {/if}
                </span>
              </div>
              <div class="flex min-w-0 flex-1 {rowBg(row, 'new')}">
                <span class="w-11 shrink-0 select-none px-2 text-right font-mono text-muted-foreground/40">
                  {row.newNumber || ""}
                </span>
                <span class="min-w-0 flex-1 pr-3 font-mono">
                  {#if has(row, "new")}
                    <CodeLine text={row.newText} against={counterpart(row, "new")} side="new" />
                  {/if}
                </span>
              </div>
            </div>
          {/each}
        {:else}
          {#each section.rows as row, i (i)}
            {#if row.op === "equal"}
              <div class="flex font-mono text-xs leading-[1.6]">
                <span class="w-11 shrink-0 select-none px-2 text-right text-muted-foreground/40">{row.oldNumber}</span>
                <span class="w-4 shrink-0 select-none text-transparent">·</span>
                <span class="min-w-0 flex-1 pr-3"><CodeLine text={row.oldText} /></span>
              </div>
            {:else}
              {#if row.op !== "insert"}
                <div class="flex bg-red-500/[0.12] font-mono text-xs leading-[1.6]">
                  <span class="w-11 shrink-0 select-none px-2 text-right text-muted-foreground/40">{row.oldNumber}</span>
                  <span class="w-4 shrink-0 select-none text-red-600 dark:text-red-400">−</span>
                  <span class="min-w-0 flex-1 pr-3">
                    <CodeLine text={row.oldText} against={counterpart(row, "old")} side="old" />
                  </span>
                </div>
              {/if}
              {#if row.op !== "delete"}
                <div class="flex bg-emerald-500/[0.12] font-mono text-xs leading-[1.6]">
                  <span class="w-11 shrink-0 select-none px-2 text-right text-muted-foreground/40">{row.newNumber}</span>
                  <span class="w-4 shrink-0 select-none text-emerald-600 dark:text-emerald-400">+</span>
                  <span class="min-w-0 flex-1 pr-3">
                    <CodeLine text={row.newText} against={counterpart(row, "new")} side="new" />
                  </span>
                </div>
              {/if}
            {/if}
          {/each}
        {/if}
      {/each}
    </div>
  {/if}
</div>
