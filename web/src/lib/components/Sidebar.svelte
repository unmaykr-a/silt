<script lang="ts">
  import { link, router } from "$lib/router.svelte";
  import type { Project } from "$lib/api/client";
  import type { Snippet } from "svelte";

  // Inline project links were fine for a handful of stacks and unusable at
  // thirty. A filtered list scales and stays navigable by keyboard.
  //
  // The rail scrolls on its own rather than making the page taller: a project
  // list forty entries long should not decide how far the timeline scrolls.
  // That is what `min-h-0` plus `overflow-y-auto` on the list buys, and it
  // only works because the shell above is a fixed-height flex column.
  let {
    projects,
    open = $bindable(),
    nav,
  }: {
    projects: Project[];
    open: boolean;
    /** Section links, rendered above the project list in the side layout. */
    nav?: Snippet;
  } = $props();

  let filter = $state("");

  const route = $derived(router.current);
  const activeProjectId = $derived(
    route.name === "project" || route.name === "service" || route.name === "files"
      ? route.projectId
      : route.name === "diff"
        ? route.projectId
        : undefined,
  );

  const matches = $derived(
    filter.trim() === ""
      ? projects
      : projects.filter((p) => p.name.toLowerCase().includes(filter.trim().toLowerCase())),
  );

  function activeClass(id: number): string {
    return id === activeProjectId
      ? "bg-secondary text-secondary-foreground"
      : "text-muted-foreground hover:bg-secondary/50 hover:text-foreground";
  }
</script>

<aside
  class="{open ? 'flex' : 'hidden'} w-60 shrink-0 flex-col overflow-hidden border-r border-border md:flex"
  aria-label="Navigation"
>
  {#if nav}
    <div class="shrink-0 border-b border-border p-2">
      {@render nav()}
    </div>
  {/if}

  <div class="shrink-0 p-2">
    <input
      type="search"
      bind:value={filter}
      placeholder="Filter projects…"
      aria-label="Filter projects"
      class="w-full rounded-md border border-border bg-background px-2.5 py-1.5 text-xs outline-none focus:ring-2 focus:ring-ring"
    />
  </div>

  <nav class="min-h-0 flex-1 overflow-y-auto px-2 pb-3" aria-label="Projects">
    <a
      use:link
      href="/projects"
      onclick={() => (open = false)}
      class="mb-1 flex items-center justify-between rounded-md px-2.5 py-1.5 text-[10px] font-medium uppercase tracking-wide
             {route.name === 'projects'
        ? 'bg-secondary text-secondary-foreground'
        : 'text-muted-foreground/60 hover:bg-secondary/50 hover:text-foreground'}"
    >
      <span>All projects</span>
      <span class="font-mono tabular-nums">{projects.length}</span>
    </a>

    {#if matches.length === 0}
      <p class="px-2.5 py-2 text-xs text-muted-foreground/70">
        {projects.length === 0 ? "None discovered yet." : "No match."}
      </p>
    {:else}
      <ul>
        {#each matches as project (project.id)}
          <li>
            <a
              use:link
              href="/projects/{project.id}"
              onclick={() => (open = false)}
              class="block truncate rounded-md px-2.5 py-1.5 text-sm {activeClass(project.id)}"
              title={project.name}
            >
              {project.name}
            </a>
          </li>
        {/each}
      </ul>
    {/if}
  </nav>
</aside>
