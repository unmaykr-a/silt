<script lang="ts">
  import { api, type Project } from "$lib/api/client";
  import { link } from "$lib/router.svelte";
  import Timestamp from "$lib/components/Timestamp.svelte";
  import Empty from "$lib/components/Empty.svelte";

  // The sidebar is a filter box and a list; on a host with thirty stacks that
  // is a scroll, not a glance. This is the page you open when you want to see
  // them all at once, sorted by what moved most recently.
  let { reloadKey }: { reloadKey: number } = $props();

  let projects = $state<Project[]>([]);
  let error = $state<string | null>(null);
  let loading = $state(true);
  let filter = $state("");
  let sort = $state<"recent" | "name">("recent");
  let showArchived = $state(false);

  $effect(() => {
    void reloadKey;
    const controller = new AbortController();
    api
      .projects(controller.signal)
      .then((p) => {
        projects = p;
        error = null;
      })
      .catch((err: Error) => {
        if (err.name !== "AbortError") error = err.message;
      })
      .finally(() => (loading = false));
    return () => controller.abort();
  });

  const shown = $derived.by(() => {
    const needle = filter.trim().toLowerCase();
    const out = projects.filter((p) => {
      if (!showArchived && p.archived) return false;
      return needle === "" || p.name.toLowerCase().includes(needle);
    });
    out.sort((a, b) =>
      sort === "name" ? a.name.localeCompare(b.name) : b.last_seen_at - a.last_seen_at,
    );
    return out;
  });

  const archivedCount = $derived(projects.filter((p) => p.archived).length);
</script>

<div class="space-y-6">
  <header class="flex flex-wrap items-end justify-between gap-3">
    <div>
      <h2 class="text-2xl font-semibold tracking-tight">Projects</h2>
      <p class="mt-1 text-sm text-muted-foreground">
        {projects.length}
        {projects.length === 1 ? "stack" : "stacks"} on this host.
      </p>
    </div>

    <div class="flex flex-wrap items-center gap-2">
      <input
        type="search"
        bind:value={filter}
        placeholder="Filter…"
        aria-label="Filter projects"
        class="w-44 rounded-md border border-border bg-background px-2.5 py-1.5 text-xs outline-none focus:ring-2 focus:ring-ring"
      />
      <div class="flex rounded-md border border-border">
        {#each [["recent", "Recent"], ["name", "Name"]] as [value, label] (value)}
          <button
            class="px-3 py-1.5 text-xs transition-colors first:rounded-l-md last:rounded-r-md
                   {sort === value ? 'bg-secondary text-secondary-foreground' : 'text-muted-foreground hover:text-foreground'}"
            onclick={() => (sort = value as "recent" | "name")}
          >
            {label}
          </button>
        {/each}
      </div>
      {#if archivedCount > 0}
        <label class="flex items-center gap-2 text-xs text-muted-foreground">
          <input type="checkbox" bind:checked={showArchived} class="accent-emerald-500" />
          Archived ({archivedCount})
        </label>
      {/if}
    </div>
  </header>

  {#if error}
    <p class="rounded border border-red-900/60 bg-red-950/40 px-4 py-3 text-sm text-red-300">{error}</p>
  {/if}

  {#if loading && projects.length === 0}
    <p class="text-sm text-muted-foreground">Loading…</p>
  {:else if shown.length === 0}
    <Empty
      title={projects.length === 0 ? "No projects discovered yet." : "No project matches that filter."}
      hint={projects.length === 0
        ? "Silt discovers a project the first time it sees a container carrying Compose labels."
        : undefined}
    />
  {:else}
    <ul class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
      {#each shown as project (project.id)}
        <li>
          <a
            use:link
            href="/projects/{project.id}"
            class="block h-full rounded-lg border border-border p-4 transition-colors hover:border-foreground/25 hover:bg-secondary/30"
          >
            <div class="flex items-baseline gap-2">
              <span class="min-w-0 truncate font-medium" title={project.name}>{project.name}</span>
              {#if project.archived}
                <span class="shrink-0 rounded bg-secondary px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
                  archived
                </span>
              {/if}
            </div>
            {#if project.working_dir}
              <p class="mt-1 truncate font-mono text-[11px] text-muted-foreground/70" title={project.working_dir}>
                {project.working_dir}
              </p>
            {/if}
            <p class="mt-3 text-xs text-muted-foreground">
              last seen <Timestamp ts={project.last_seen_at} />
            </p>
          </a>
        </li>
      {/each}
    </ul>
  {/if}
</div>
