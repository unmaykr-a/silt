<script lang="ts">
  import { api, type SearchResults } from "$lib/api/client";
  import { link } from "$lib/router.svelte";
  import Empty from "$lib/components/Empty.svelte";
  import Timestamp from "$lib/components/Timestamp.svelte";
  import { severityDot } from "$lib/format";

  // One page for every kind of thing Silt records, because "when did anything
  // about radarr change?" does not know in advance whether the answer is a
  // project, a service, an environment key, a file or an event.
  let { query }: { query: string } = $props();

  let results = $state<SearchResults | null>(null);
  let error = $state<string | null>(null);
  let loading = $state(false);

  $effect(() => {
    const term = query.trim();
    if (term.length < 2) {
      results = null;
      return;
    }
    const controller = new AbortController();
    loading = true;
    // Typing in the box navigates on every keystroke, so the request is
    // debounced here rather than firing one per character.
    const timer = setTimeout(() => {
      api
        .search(term, controller.signal)
        .then((r) => {
          results = r;
          error = null;
        })
        .catch((err: Error) => {
          if (err.name !== "AbortError") error = err.message;
        })
        .finally(() => (loading = false));
    }, 180);

    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  });

  const rowClass =
    "flex items-baseline gap-3 border-b border-border/60 py-2 text-sm transition-colors hover:bg-secondary/30";
</script>

{#snippet section(title: string, count: number)}
  <h3 class="mb-1 mt-6 flex items-baseline gap-2 text-xs font-medium uppercase tracking-wide text-muted-foreground first:mt-0">
    {title}
    <span class="font-mono tabular-nums text-muted-foreground/50">{count}</span>
  </h3>
{/snippet}

<div class="space-y-2">
  <header>
    <h2 class="text-2xl font-semibold tracking-tight">
      {query.trim() ? `Results for “${query.trim()}”` : "Search"}
    </h2>
    {#if results}
      <p class="mt-1 text-sm text-muted-foreground">
        {results.total === 0 ? "Nothing matched." : `${results.total} ${results.total === 1 ? "result" : "results"}`}
        {#if results.total >= 25}
          <span class="text-muted-foreground/60">· each category is capped at 25</span>
        {/if}
      </p>
    {/if}
  </header>

  {#if error}
    <p class="rounded-md border border-red-500/40 bg-red-500/10 px-4 py-2.5 text-sm text-red-500 dark:text-red-300">
      {error}
    </p>
  {:else if query.trim().length < 2}
    <Empty
      title="Type at least two characters."
      hint="Silt looks through project names, services, environment keys, captured file paths and events. Environment values are never searched — they are keyed digests."
    />
  {:else if loading && !results}
    <p class="text-sm text-muted-foreground">Searching…</p>
  {:else if results && results.total === 0}
    <Empty
      title="Nothing matched “{query.trim()}”."
      hint="Silt searches names and keys, not values. A secret's value was never stored, so it cannot be found by its content."
    />
  {:else if results}
    {#if results.projects.length > 0}
      {@render section("Projects", results.projects.length)}
      {#each results.projects as project (project.id)}
        <a use:link href="/projects/{project.id}" class={rowClass}>
          <span class="w-1 shrink-0 self-stretch rounded-sm bg-emerald-500"></span>
          <span class="font-medium">{project.name}</span>
          {#if project.archived}
            <span class="rounded bg-secondary px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
              archived
            </span>
          {/if}
          {#if project.working_dir}
            <span class="min-w-0 truncate font-mono text-xs text-muted-foreground/60">{project.working_dir}</span>
          {/if}
          <Timestamp ts={project.last_seen_at} class="ml-auto shrink-0 text-xs text-muted-foreground" />
        </a>
      {/each}
    {/if}

    {#if results.services.length > 0}
      {@render section("Services", results.services.length)}
      {#each results.services as item (item.project_id + "/" + item.service)}
        <a use:link href="/projects/{item.project_id}/services/{encodeURIComponent(item.service)}" class={rowClass}>
          <span class="w-1 shrink-0 self-stretch rounded-sm bg-sky-500"></span>
          <span class="font-medium">{item.service}</span>
          <span class="text-muted-foreground">in {item.project_name}</span>
          <Timestamp ts={item.last_seen_at} class="ml-auto shrink-0 text-xs text-muted-foreground" />
        </a>
      {/each}
    {/if}

    {#if results.env_keys.length > 0}
      {@render section("Environment keys", results.env_keys.length)}
      {#each results.env_keys as item, i (i)}
        <a use:link href="/projects/{item.project_id}/services/{encodeURIComponent(item.service)}" class={rowClass}>
          <span class="w-1 shrink-0 self-stretch rounded-sm {item.readable ? 'bg-zinc-400' : 'bg-rose-500'}"></span>
          <span class="font-mono text-xs">{item.key}</span>
          <span class="min-w-0 truncate text-muted-foreground">{item.project_name} · {item.service}</span>
          {#if !item.readable}
            <span
              class="shrink-0 text-[11px] text-muted-foreground/60"
              title="The value is a keyed digest; it was never stored"
            >
              redacted
            </span>
          {/if}
          <Timestamp ts={item.last_seen_at} class="ml-auto shrink-0 text-xs text-muted-foreground" />
        </a>
      {/each}
    {/if}

    {#if results.files.length > 0}
      {@render section("Files", results.files.length)}
      {#each results.files as item, i (i)}
        <a
          use:link
          href="/projects/{item.project_id}/files?path={encodeURIComponent(item.path)}"
          class={rowClass}
        >
          <span class="w-1 shrink-0 self-stretch rounded-sm bg-violet-500"></span>
          <span class="min-w-0 truncate font-mono text-xs">{item.path}</span>
          <span class="shrink-0 text-muted-foreground">{item.project_name}</span>
          <Timestamp ts={item.last_seen_at} class="ml-auto shrink-0 text-xs text-muted-foreground" />
        </a>
      {/each}
    {/if}

    {#if results.events.length > 0}
      {@render section("Events", results.events.length)}
      {#each results.events as event (event.id)}
        <div class={rowClass}>
          <span class="w-1 shrink-0 self-stretch rounded-sm {severityDot(event.severity)}"></span>
          <span class="shrink-0 font-mono text-xs">{event.type}</span>
          {#if event.service}
            <span class="shrink-0 text-muted-foreground">{event.service}</span>
          {/if}
          {#if event.message}
            <span class="min-w-0 flex-1 truncate text-xs text-muted-foreground/70" title={event.message}>
              {event.message}
            </span>
          {/if}
          <Timestamp ts={event.ts} class="ml-auto shrink-0 text-xs text-muted-foreground" />
        </div>
      {/each}
    {/if}
  {/if}
</div>
