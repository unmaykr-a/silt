<script lang="ts">
  import { link, router } from "$lib/router.svelte";
  import type { Project } from "$lib/api/client";

  // Inline project links were fine for a handful of stacks and unusable at
  // thirty: they wrapped over six lines and pushed the content off screen. A
  // filtered list scales, and stays navigable by keyboard.
  let {
    projects,
    open = $bindable(),
  }: { projects: Project[]; open: boolean } = $props();

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
  class="{open ? 'flex' : 'hidden'} w-60 shrink-0 flex-col border-r border-border md:flex"
  aria-label="Projects"
>
  <div class="p-3">
    <input
      type="search"
      bind:value={filter}
      placeholder="Filter {projects.length} projects…"
      aria-label="Filter projects"
      class="w-full rounded-md border border-border bg-background px-2.5 py-1.5 text-xs outline-none focus:ring-2 focus:ring-ring"
    />
  </div>

  <nav class="min-h-0 flex-1 overflow-y-auto px-2 pb-3">
    <a
      use:link
      href="/"
      onclick={() => (open = false)}
      class="block rounded-md px-2.5 py-1.5 text-sm {route.name === 'timeline'
        ? 'bg-secondary text-secondary-foreground'
        : 'text-muted-foreground hover:bg-secondary/50 hover:text-foreground'}"
    >
      Timeline
    </a>

    <p class="mt-4 px-2.5 pb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground/60">
      Projects
    </p>

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

    <a
      use:link
      href="/settings"
      onclick={() => (open = false)}
      class="mt-4 block rounded-md px-2.5 py-1.5 text-sm {route.name === 'settings'
        ? 'bg-secondary text-secondary-foreground'
        : 'text-muted-foreground hover:bg-secondary/50 hover:text-foreground'}"
    >
      Settings
    </a>
  </nav>
</aside>
