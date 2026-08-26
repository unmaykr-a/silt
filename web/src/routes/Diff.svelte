<script lang="ts">
  import { api, type Diff, type Snapshot, type Change } from "$lib/api/client";
  import { link, router } from "$lib/router.svelte";
  import Timestamp from "$lib/components/Timestamp.svelte";
  import Empty from "$lib/components/Empty.svelte";
  import YamlDiff from "$lib/components/YamlDiff.svelte";
  import { severityDot, datetime } from "$lib/format";
  import { Badge } from "$lib/components/ui/badge";
  import * as Tabs from "$lib/components/ui/tabs";

  let {
    from,
    to,
    projectId,
  }: { from?: number; to?: number; projectId?: number } = $props();

  let diff = $state<Diff | null>(null);
  let snapshots = $state<Snapshot[]>([]);
  let error = $state<string | null>(null);
  let loading = $state(true);
  let view = $state("structured");
  let yamlBefore = $state("");
  let yamlAfter = $state("");
  let yamlLoading = $state(false);
  // Set by resolve() to explain why a pair could not be formed.
  let unresolved = "";

  // Resolve a missing `from` to the previous snapshot, so a timeline entry can
  // link straight to its own diff without knowing what came before it.
  async function resolve(signal: AbortSignal): Promise<[number, number] | null> {
    if (from && to) return [from, to];
    if (!to || !projectId) return null;
    const list = await api.snapshots(projectId, { limit: 200 }, signal);
    snapshots = list;
    const index = list.findIndex((s) => s.id === to);
    if (index < 0) {
      unresolved = `Snapshot #${to} does not belong to this project.`;
      return null;
    }
    const previous = list[index + 1];
    if (!previous) {
      unresolved = "This is the earliest snapshot, so there is nothing to compare it against.";
      return null;
    }
    return [previous.id, to];
  }

  $effect(() => {
    const key = [from, to, projectId];
    void key;

    const controller = new AbortController();
    loading = true;
    (async () => {
      try {
        unresolved = "";
        const pair = await resolve(controller.signal);
        if (!pair) {
          error = unresolved || "Pick two snapshots to compare.";
          diff = null;
          return;
        }
        diff = await api.diff(pair[0], pair[1], controller.signal);
        if (projectId && snapshots.length === 0) {
          snapshots = await api.snapshots(projectId, { limit: 200 }, controller.signal);
        }
        error = null;
      } catch (err) {
        if ((err as Error).name !== "AbortError") error = (err as Error).message;
      } finally {
        loading = false;
      }
    })();
    return () => controller.abort();
  });

  // YAML is fetched only when that view is opened; most visits never need it.
  $effect(() => {
    if (view !== "yaml" || !diff) return;
    const controller = new AbortController();
    yamlLoading = true;
    Promise.all([
      api.composeYaml(diff.from.id, controller.signal),
      api.composeYaml(diff.to.id, controller.signal),
    ])
      .then(([a, b]) => {
        yamlBefore = a;
        yamlAfter = b;
      })
      .catch(() => {})
      .finally(() => (yamlLoading = false));
    return () => controller.abort();
  });

  // Grouped by service, then by kind — the order someone reads a diff in.
  const grouped = $derived.by(() => {
    const out = new Map<string, Map<string, Change[]>>();
    for (const change of diff?.changes ?? []) {
      const service = change.service || "project";
      if (!out.has(service)) out.set(service, new Map());
      const byKind = out.get(service)!;
      if (!byKind.has(change.kind)) byKind.set(change.kind, []);
      byKind.get(change.kind)!.push(change);
    }
    return out;
  });

  function reselect(which: "from" | "to", value: string) {
    const id = Number(value);
    const other = which === "from" ? diff?.to.id : diff?.from.id;
    if (!id || !other) return;
    const f = which === "from" ? id : other;
    const t = which === "from" ? other : id;
    router.navigate(`/diff?from=${f}&to=${t}${projectId ? `&project=${projectId}` : ""}`);
  }
</script>

<div class="space-y-6">
  <header class="flex flex-wrap items-baseline justify-between gap-3">
    <h2 class="text-2xl font-semibold tracking-tight">Diff</h2>
    {#if diff}
      <Tabs.Root bind:value={view}>
        <Tabs.List>
          <Tabs.Trigger value="structured">Structured</Tabs.Trigger>
          <Tabs.Trigger value="yaml">YAML</Tabs.Trigger>
        </Tabs.List>
      </Tabs.Root>
    {/if}
  </header>

  {#if snapshots.length > 0 && diff}
    <div class="flex flex-wrap items-center gap-2 text-xs">
      <select
        class="rounded-md border border-border bg-background px-2 py-1.5"
        value={String(diff.from.id)}
        onchange={(e) => reselect("from", e.currentTarget.value)}
      >
        {#each snapshots as s (s.id)}
          <option value={String(s.id)}>#{s.id} · {datetime(s.taken_at)}</option>
        {/each}
      </select>
      <span class="text-muted-foreground">→</span>
      <select
        class="rounded-md border border-border bg-background px-2 py-1.5"
        value={String(diff.to.id)}
        onchange={(e) => reselect("to", e.currentTarget.value)}
      >
        {#each snapshots as s (s.id)}
          <option value={String(s.id)}>#{s.id} · {datetime(s.taken_at)}</option>
        {/each}
      </select>
      {#if projectId}
        <a use:link href="/projects/{projectId}" class="ml-2 text-muted-foreground underline-offset-4 hover:underline">
          back to project
        </a>
      {/if}
    </div>
  {/if}

  {#if error}
    <p class="rounded border border-amber-900/60 bg-amber-950/30 px-4 py-3 text-sm text-amber-200">{error}</p>
  {:else if loading}
    <p class="text-sm text-muted-foreground">Loading…</p>
  {:else if diff}
    <div class="flex flex-wrap items-center gap-4 text-xs text-muted-foreground">
      <span>
        <Timestamp ts={diff.from.taken_at} /> → <Timestamp ts={diff.to.taken_at} />
      </span>
      {#each Object.entries(diff.summary ?? {}) as [kind, count] (kind)}
        <span class="font-mono">{kind}: {count}</span>
      {/each}
    </div>

    {#if view === "yaml"}
      {#if yamlLoading && yamlBefore === ""}
        <p class="text-sm text-muted-foreground">Loading…</p>
      {:else}
        <!-- Two whole documents side by side with nothing marked left the
             reader to find the changed line themselves. The lines that moved
             are tinted, the words inside them are marked, and the long
             unchanged runs between them collapse. -->
        <YamlDiff
          before={yamlBefore}
          after={yamlAfter}
          beforeLabel="#{diff.from.id}"
          afterLabel="#{diff.to.id}"
        />
      {/if}
    {:else if diff.changes.length === 0}
      <Empty title="These two snapshots are identical." />
    {:else}
      <div class="space-y-6">
        {#each [...grouped] as [service, byKind] (service)}
          <section>
            <h3 class="text-sm font-medium">{service}</h3>
            <div class="mt-2 space-y-3">
              {#each [...byKind] as [kind, changes] (kind)}
                <div class="rounded-lg border border-border">
                  <div class="flex items-center gap-2 border-b border-border px-3 py-2">
                    <span class="size-1.5 rounded-full {severityDot(changes[0].severity)}" aria-hidden="true"></span>
                    <span class="font-mono text-xs">{kind}</span>
                    <Badge
                      variant={changes[0].severity === "high" ? "destructive" : "secondary"}
                      class="text-[10px]"
                    >
                      {changes[0].severity}
                    </Badge>
                  </div>
                  <ul class="divide-y divide-border">
                    {#each changes as change (change.path + change.op + (change.before ?? "") + (change.after ?? ""))}
                      <li class="px-3 py-2 text-xs">
                        <p class="font-mono text-muted-foreground">{change.path}</p>
                        <div class="mt-1 grid gap-1 sm:grid-cols-2">
                          {#if change.before}
                            <p class="break-all font-mono text-red-300/90">− {change.before}</p>
                          {:else}
                            <p class="text-muted-foreground/50">−</p>
                          {/if}
                          {#if change.after}
                            <p class="break-all font-mono text-emerald-300/90">+ {change.after}</p>
                          {:else}
                            <p class="text-muted-foreground/50">+</p>
                          {/if}
                        </div>
                      </li>
                    {/each}
                  </ul>
                </div>
              {/each}
            </div>
          </section>
        {/each}
      </div>
    {/if}
  {/if}
</div>
