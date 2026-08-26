<script lang="ts">
  import {
    api,
    type Snapshot,
    type FileDiff,
    type PreviewLine,
    type RedactionRule,
  } from "$lib/api/client";
  import { link } from "$lib/router.svelte";
  import Timestamp from "$lib/components/Timestamp.svelte";
  import Empty from "$lib/components/Empty.svelte";
  import { Button } from "$lib/components/ui/button";
  import * as Tabs from "$lib/components/ui/tabs";

  let { projectId, initialPath }: { projectId: number; initialPath?: string } = $props();

  let paths = $state<string[]>([]);
  // Content hashes per snapshot, so the picker can say which files changed
  // without fetching every file's text.
  let hashes = $state<Record<number, Record<string, string>>>({});
  let path = $state(initialPath ?? "");
  let snapshots = $state<Snapshot[]>([]);
  let view = $state("changes");
  let error = $state<string | null>(null);

  let content = $state("");
  let diff = $state<FileDiff | null>(null);
  let fullContext = $state(false);
  let fromId = $state(0);
  let toId = $state(0);

  let loadingDiff = $state(false);
  // A plain variable, not $state: reading it must not make the load effect
  // depend on its own writes, which is what left the selectors unset.
  let initialised = false;

  let preview = $state<PreviewLine[]>([]);
  let previewError = $state<string | null>(null);
  let rules = $state<RedactionRule[]>([]);
  let busyLine = $state<number | null>(null);

  $effect(() => {
    const controller = new AbortController();
    Promise.all([
      api.projectFiles(projectId, controller.signal),
      api.snapshots(projectId, { limit: 200 }, controller.signal),
      api.redactionRules(projectId, controller.signal),
    ])
      .then(([p, snaps, r]) => {
        paths = p;
        snapshots = snaps;
        rules = r;
        if (!path && p.length > 0) path = p[0];
        // Default to the two most recent snapshots, once.
        if (!initialised) {
          initialised = true;
          toId = snaps[0]?.id ?? 0;
          fromId = snaps[1]?.id ?? 0;
        }
        error = null;
      })
      .catch((err: Error) => {
        if (err.name !== "AbortError") error = err.message;
      });
    return () => controller.abort();
  });

  // Load the file inventory for whichever snapshots are selected.
  $effect(() => {
    const key = [fromId, toId];
    void key;
    const controller = new AbortController();
    for (const id of [fromId, toId]) {
      if (!id || hashes[id]) continue;
      api
        .snapshotFiles(id, controller.signal)
        .then((files) => {
          hashes = {
            ...hashes,
            [id]: Object.fromEntries(files.map((f) => [f.path, f.content_hash ?? ""])),
          };
        })
        .catch(() => {});
    }
    return () => controller.abort();
  });

  // A file that did not change between the two snapshots is not what someone
  // came here to read, so mark the ones that did and open one of them.
  const changedPaths = $derived.by(() => {
    const before = hashes[fromId];
    const after = hashes[toId];
    if (!before || !after) return new Set<string>();
    const out = new Set<string>();
    for (const p of paths) {
      if ((before[p] ?? "") !== (after[p] ?? "")) out.add(p);
    }
    return out;
  });

  let autoSelected = false;
  $effect(() => {
    if (autoSelected || view !== "changes" || changedPaths.size === 0) return;
    if (!changedPaths.has(path)) {
      autoSelected = true;
      path = [...changedPaths][0];
    }
  });

  $effect(() => {
    const key = [path, toId, fromId, view, fullContext];
    void key;
    if (!path) return;
    const controller = new AbortController();

    if (view === "current" && toId) {
      api
        .snapshotFile(toId, path, controller.signal)
        .then((f) => (content = f.content))
        .catch(() => (content = ""));
    } else if (view === "changes" && fromId && toId) {
      loadingDiff = true;
      api
        .fileDiff(fromId, toId, path, fullContext, controller.signal)
        .then((d) => {
          diff = d;
          loadingDiff = false;
        })
        .catch((err: Error) => {
          if (err.name === "AbortError") return;
          diff = null;
          loadingDiff = false;
        });
    }
    return () => controller.abort();
  });

  // The marking view reads the file live rather than from a capture: a capture
  // is already redacted, so there would be nothing left to decide about.
  $effect(() => {
    const key = [path, view, rules.length];
    void key;
    if (view !== "marking" || !path) return;
    const controller = new AbortController();
    api
      .filePreview(projectId, path, controller.signal)
      .then((p) => {
        preview = p.lines;
        previewError = null;
      })
      .catch((err: Error) => {
        if (err.name !== "AbortError") previewError = err.message;
      });
    return () => controller.abort();
  });

  async function toggleLine(line: PreviewLine) {
    busyLine = line.number;
    try {
      // A key rule survives edits that move the line; fall back to the line
      // number only when there is no key to hang it on.
      const rule = line.key
        ? { path, action: (line.redacted ? "reveal" : "hide") as "hide" | "reveal", kind: "key" as const, key: line.key }
        : { path, action: (line.redacted ? "reveal" : "hide") as "hide" | "reveal", kind: "line" as const, line_no: line.number };
      await api.addRedactionRule(projectId, rule);
      rules = await api.redactionRules(projectId);
      previewError = null;
    } catch (err) {
      previewError = (err as Error).message;
    } finally {
      busyLine = null;
    }
  }

  async function removeRule(id: number) {
    await api.deleteRedactionRule(projectId, id);
    rules = await api.redactionRules(projectId);
  }

  function lineClass(line: PreviewLine): string {
    if (!line.markable) return "text-muted-foreground/60";
    if (line.reason === "rule_hide") return "bg-red-950/30 text-red-200";
    if (line.reason === "rule_reveal") return "bg-emerald-950/30 text-emerald-200";
    if (line.redacted) return "text-muted-foreground";
    return "text-foreground";
  }

  function reasonLabel(line: PreviewLine): string {
    switch (line.reason) {
      case "rule_hide":
        return "hidden by you";
      case "rule_reveal":
        return "shown by you";
      case "keep_list":
        return "safe key";
      case "interpolation":
        return "variable reference";
      case "default":
        return "hidden by default";
      default:
        return "";
    }
  }
</script>

<div class="space-y-5">
  <header class="flex flex-wrap items-baseline justify-between gap-3">
    <div>
      <a use:link href="/projects/{projectId}" class="text-xs text-muted-foreground underline-offset-4 hover:underline">
        ← back to project
      </a>
      <h2 class="mt-1 text-2xl font-semibold tracking-tight">Compose files</h2>
    </div>
    <Tabs.Root bind:value={view}>
      <Tabs.List>
        <Tabs.Trigger value="changes">Changes</Tabs.Trigger>
        <Tabs.Trigger value="current">Full file</Tabs.Trigger>
        <Tabs.Trigger value="marking">What to hide</Tabs.Trigger>
      </Tabs.List>
    </Tabs.Root>
  </header>

  {#if error}
    <p class="rounded border border-red-900/60 bg-red-950/40 px-4 py-3 text-sm text-red-300">{error}</p>
  {/if}

  {#if paths.length === 0}
    <Empty
      title="No compose files captured."
      hint="Mount your compose directories read-only and set SILT_COMPOSE_ROOTS to capture them."
    />
  {:else}
    <div class="flex flex-wrap items-center gap-2 text-xs">
      <select bind:value={path} class="max-w-lg rounded-md border border-border bg-background px-2 py-1.5 font-mono">
        {#each paths as p (p)}
          <option value={p}>{changedPaths.has(p) ? "● " : ""}{p}</option>
        {/each}
      </select>

      {#if view === "changes" && snapshots.length >= 2}
        <select bind:value={fromId} class="rounded-md border border-border bg-background px-2 py-1.5">
          {#each snapshots as s (s.id)}
            <option value={s.id}>#{s.id} · {new Date(s.taken_at).toLocaleString()}</option>
          {/each}
        </select>
        <span class="text-muted-foreground">→</span>
        <select bind:value={toId} class="rounded-md border border-border bg-background px-2 py-1.5">
          {#each snapshots as s (s.id)}
            <option value={s.id}>#{s.id} · {new Date(s.taken_at).toLocaleString()}</option>
          {/each}
        </select>
        <label class="flex items-center gap-2 text-muted-foreground">
          <input type="checkbox" bind:checked={fullContext} class="accent-emerald-500" />
          Whole file
        </label>
      {/if}
    </div>

    {#if view === "changes"}
      {#if snapshots.length < 2}
        <Empty
          title="Only one snapshot so far, so there is nothing to compare against."
          hint="Silt captures your compose files on every change; come back after the next one, or use Full file to read the current capture."
        />
      {:else if loadingDiff}
        <p class="text-sm text-muted-foreground">Loading…</p>
      {:else if !diff}
        <Empty title="This file was not captured in one of the selected snapshots." />
      {:else if diff.diff.identical}
        <Empty title="This file is identical between the two snapshots." />
      {:else}
        <p class="text-xs text-muted-foreground">
          <span class="text-emerald-400">+{diff.diff.added}</span>
          <span class="ml-2 text-red-400">−{diff.diff.removed}</span>
          <span class="ml-3">
            <Timestamp ts={diff.from.taken_at} /> → <Timestamp ts={diff.to.taken_at} />
          </span>
        </p>
        <div class="overflow-x-auto rounded-lg border border-border bg-card">
          {#each diff.diff.hunks as hunk, i (i)}
            {#if i > 0}
              <div class="border-y border-border bg-secondary/30 px-3 py-1 font-mono text-[11px] text-muted-foreground">
                @@ −{hunk.old_start},{hunk.old_count} +{hunk.new_start},{hunk.new_count} @@
              </div>
            {/if}
            {#each hunk.lines as line, j (j)}
              <div
                class="flex font-mono text-xs leading-relaxed {line.op === 'insert'
                  ? 'bg-emerald-950/40'
                  : line.op === 'delete'
                    ? 'bg-red-950/40'
                    : ''}"
              >
                <span class="w-12 shrink-0 select-none px-2 text-right text-muted-foreground/50">
                  {line.old_number || ""}
                </span>
                <span class="w-12 shrink-0 select-none px-2 text-right text-muted-foreground/50">
                  {line.new_number || ""}
                </span>
                <span
                  class="w-4 shrink-0 select-none {line.op === 'insert'
                    ? 'text-emerald-400'
                    : line.op === 'delete'
                      ? 'text-red-400'
                      : 'text-transparent'}"
                >
                  {line.op === "insert" ? "+" : line.op === "delete" ? "−" : " "}
                </span>
                <span class="whitespace-pre pr-4">{line.text}</span>
              </div>
            {/each}
          {/each}
        </div>
      {/if}
    {:else if view === "current"}
      <div class="overflow-x-auto rounded-lg border border-border bg-card">
        {#each content.split("\n") as line, i (i)}
          <div class="flex font-mono text-xs leading-relaxed">
            <span class="w-12 shrink-0 select-none px-2 text-right text-muted-foreground/50">{i + 1}</span>
            <span class="whitespace-pre pr-4">{line}</span>
          </div>
        {/each}
      </div>
    {:else}
      <div class="rounded-lg border border-border bg-secondary/20 px-4 py-3 text-xs text-muted-foreground">
        <p>
          This reads the file live and stores nothing. Click any line to change what happens to it
          <em>next time it is captured</em>: hide a value the safe-key list missed, or show one it
          hid unnecessarily.
        </p>
        <p class="mt-1">
          Hiding takes effect before anything is written, so a hidden value is never stored.
          Showing applies only to future captures — earlier snapshots hold a digest, not the value,
          so there is nothing there to uncover.
        </p>
      </div>

      {#if previewError}
        <p class="rounded border border-amber-900/60 bg-amber-950/30 px-4 py-3 text-sm text-amber-200">
          {previewError}
        </p>
      {:else}
        <div class="overflow-x-auto rounded-lg border border-border bg-card">
          {#each preview as line (line.number)}
            <div
              class="group flex items-baseline font-mono text-xs leading-relaxed {lineClass(line)} {line.markable
                ? 'cursor-pointer hover:bg-secondary/40'
                : ''}"
              role={line.markable ? "button" : undefined}
              tabindex={line.markable ? 0 : undefined}
              onclick={() => line.markable && toggleLine(line)}
              onkeydown={(e) => {
                if (line.markable && (e.key === "Enter" || e.key === " ")) {
                  e.preventDefault();
                  toggleLine(line);
                }
              }}
            >
              <span class="w-12 shrink-0 select-none px-2 text-right text-muted-foreground/50">
                {line.number}
              </span>
              <span class="min-w-0 flex-1 whitespace-pre pr-4">{line.text}</span>
              {#if line.markable}
                <span class="shrink-0 px-2 text-[10px] text-muted-foreground/60">{reasonLabel(line)}</span>
                <!-- Visible rather than hover-only: nothing else signals that
                     these lines can be clicked at all. -->
                <span
                  class="mr-3 shrink-0 rounded border border-border px-2 py-0.5 text-[10px] {line.redacted
                    ? 'text-emerald-400/70'
                    : 'text-red-400/70'} opacity-70 transition-opacity group-hover:opacity-100"
                  aria-hidden="true"
                >
                  {busyLine === line.number ? "…" : line.redacted ? "show" : "hide"}
                </span>
              {/if}
            </div>
          {/each}
        </div>

        {#if rules.length > 0}
          <section>
            <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">
              Your decisions ({rules.length})
            </h3>
            <ul class="mt-2 divide-y divide-border border-y border-border">
              {#each rules as rule (rule.id)}
                <li class="flex items-baseline gap-3 py-2 text-xs">
                  <span class={rule.action === "hide" ? "text-red-400" : "text-emerald-400"}>
                    {rule.action}
                  </span>
                  <span class="font-mono">{rule.key || `line ${rule.line_no}`}</span>
                  <span class="min-w-0 truncate font-mono text-muted-foreground/60">
                    {rule.path || "all files"}
                  </span>
                  <Button
                    variant="ghost"
                    size="sm"
                    class="ml-auto h-6 text-[10px]"
                    onclick={() => removeRule(rule.id)}
                  >
                    remove
                  </Button>
                </li>
              {/each}
            </ul>
          </section>
        {/if}
      {/if}
    {/if}
  {/if}
</div>
