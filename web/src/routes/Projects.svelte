<script lang="ts">
  import { api, type Overview, type ProjectOverview } from "$lib/api/client";
  import { link } from "$lib/router.svelte";
  import Timestamp from "$lib/components/Timestamp.svelte";
  import Empty from "$lib/components/Empty.svelte";
  import type { StateKey } from "$lib/servicestate";

  // The fleet view.
  //
  // This screen used to be a card per stack carrying its name and when it was
  // last seen — forty-seven cards all saying "2m ago", which is a directory
  // rather than an answer. What someone opens it for is: what is down, what is
  // unhealthy, what has been restarting, and what did I edit and forget to
  // apply. Everything else is secondary to that.
  let { reloadKey }: { reloadKey: number } = $props();

  // One lens per failure mode, because they are different failure modes.
  // "not running" used to cover all of them, which meant a container someone
  // stopped on purpose and a container in a crash loop were the same filter.
  type Lens =
    | "all"
    | "attention"
    | "unhealthy"
    | "crashed"
    | "restarting"
    | "stopped"
    | "drift"
    | "restarts";

  let data = $state<Overview | null>(null);
  let error = $state<string | null>(null);
  let loading = $state(true);
  let filter = $state("");
  let sort = $state<"attention" | "recent" | "name">("attention");
  let lens = $state<Lens>("all");
  let showArchived = $state(false);

  $effect(() => {
    void reloadKey;
    const controller = new AbortController();
    api
      .overview(controller.signal)
      .then((o) => {
        data = o;
        error = null;
      })
      .catch((err: Error) => {
        if (err.name !== "AbortError") error = err.message;
      })
      .finally(() => (loading = false));
    return () => controller.abort();
  });

  const projects = $derived(data?.projects ?? []);
  const totals = $derived(data?.totals);
  const archivedCount = $derived(projects.filter((p) => p.archived).length);

  function matchesLens(p: ProjectOverview): boolean {
    switch (lens) {
      case "attention":
        return p.attention;
      case "unhealthy":
        return p.unhealthy > 0;
      case "crashed":
        return p.crashed > 0 || p.oom_killed > 0;
      case "restarting":
        return p.restarting > 0;
      case "stopped":
        return p.stopped > 0;
      case "drift":
        return p.drift;
      case "restarts":
        return p.max_restart_count > 0;
      default:
        return true;
    }
  }

  // Severity order. A container that is up and answering wrongly outranks one
  // that crashed, which outranks a loop, which outranks an edit nobody has
  // applied. A container stopped on purpose scores nothing at all: it is a
  // state, not a fault, and sorting it above a healthy stack would be noise.
  function weight(p: ProjectOverview): number {
    return (
      p.unhealthy * 10_000 +
      p.oom_killed * 5_000 +
      p.crashed * 1_000 +
      p.restarting * 100 +
      (p.drift ? 10 : 0) +
      (p.max_restart_count > 0 ? 1 : 0)
    );
  }

  const shown = $derived.by(() => {
    const needle = filter.trim().toLowerCase();
    const out = projects.filter((p) => {
      if (!showArchived && p.archived) return false;
      if (!matchesLens(p)) return false;
      return needle === "" || p.name.toLowerCase().includes(needle);
    });
    out.sort((a, b) => {
      if (sort === "name") return a.name.localeCompare(b.name);
      if (sort === "recent") return b.last_seen_at - a.last_seen_at;
      const byWeight = weight(b) - weight(a);
      return byWeight !== 0 ? byWeight : a.name.localeCompare(b.name);
    });
    return out;
  });

  // Each stat is a filter rather than a decoration: seeing "3 unhealthy" and
  // then having to hunt for which three is the state this screen replaced.
  function toggleLens(next: Lens) {
    lens = lens === next ? "all" : next;
  }

  const stats = $derived.by(() => {
    if (!totals) return [];
    // Each chip names one thing. "Not running" covered four of them and so
    // named none of them. Counts are containers except where the label says
    // stacks: drift and restart history belong to a project, not a container.
    return [
      { key: "attention" as Lens, label: "stacks need attention", count: totals.attention, tone: "amber" },
      { key: "unhealthy" as Lens, label: "unhealthy", count: totals.unhealthy, tone: "red" },
      { key: "crashed" as Lens, label: "crashed", count: totals.crashed, tone: "orange" },
      { key: "restarting" as Lens, label: "restarting", count: totals.restarting, tone: "amber" },
      // Grey and last: a container someone stopped is a state, not a fault.
      { key: "stopped" as Lens, label: "stopped", count: totals.stopped - totals.crashed, tone: "zinc" },
      { key: "drift" as Lens, label: "stacks with unapplied edits", count: totals.drift, tone: "sky" },
    ].filter((s) => s.count > 0);
  });

  // What is wrong with this stack, as separate badges rather than one number.
  // Colour comes from the shared vocabulary so a badge here and a dot on the
  // service page mean the same thing.
  type Badge = { label: string; title: string; class: string };

  function badges(p: ProjectOverview): Badge[] {
    const out: Badge[] = [];
    const chip = (key: StateKey, label: string, title: string) => {
      const tone = {
        unhealthy: "bg-red-500/10 text-red-600 dark:text-red-400",
        oom: "bg-fuchsia-500/10 text-fuchsia-600 dark:text-fuchsia-400",
        crashed: "bg-orange-500/10 text-orange-600 dark:text-orange-400",
        restarting: "bg-amber-500/10 text-amber-600 dark:text-amber-400",
        stopped: "bg-secondary text-muted-foreground",
        paused: "bg-violet-500/10 text-violet-600 dark:text-violet-400",
        starting: "bg-sky-500/10 text-sky-600 dark:text-sky-400",
        running: "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
        unknown: "bg-secondary text-muted-foreground",
      }[key];
      out.push({ label, title, class: tone });
    };

    if (p.unhealthy > 0) {
      chip("unhealthy", `${p.unhealthy} unhealthy`, "Running, but failing a healthcheck.");
    }
    if (p.oom_killed > 0) {
      chip("oom", `${p.oom_killed} OOM-killed`, "Killed by the kernel for exceeding a memory limit.");
    }
    // An OOM kill is already counted as crashed; showing both would double-count.
    const crashed = p.crashed - p.oom_killed;
    if (crashed > 0) {
      chip("crashed", `${crashed} crashed`, "Stopped with a non-zero exit code. Nobody asked it to.");
    }
    if (p.restarting > 0) {
      chip("restarting", `${p.restarting} restarting`, "In a restart loop, or coming back up.");
    }
    // Deliberately last and deliberately grey: a container someone stopped is
    // a state, not a fault.
    const stopped = p.stopped - p.crashed;
    if (stopped > 0) {
      chip("stopped", `${stopped} stopped`, "Exited cleanly. Nothing went wrong.");
    }
    if (p.paused > 0) {
      chip("paused", `${p.paused} paused`, "Suspended with docker pause.");
    }
    if (p.max_restart_count > 0) {
      chip(
        "restarting",
        `${p.max_restart_count} restarts`,
        "Highest restart count in this stack, since the container was created.",
      );
    }
    if (p.drift) {
      out.push({
        label: "unapplied edit",
        title: "A compose file on disk differs from the one that was applied.",
        class: "bg-sky-500/10 text-sky-600 dark:text-sky-400",
      });
    }
    return out;
  }

  const toneClass: Record<string, string> = {
    red: "text-red-600 dark:text-red-400",
    orange: "text-orange-600 dark:text-orange-400",
    amber: "text-amber-600 dark:text-amber-400",
    sky: "text-sky-600 dark:text-sky-400",
    zinc: "text-muted-foreground",
  };
</script>

<div class="space-y-6">
  <header class="flex flex-wrap items-end justify-between gap-3">
    <div>
      <h2 class="text-2xl font-semibold tracking-tight">Projects</h2>
      <p class="mt-1 text-sm text-muted-foreground">
        {#if totals}
          {totals.projects}
          {totals.projects === 1 ? "stack" : "stacks"}, {totals.running} of {totals.services} containers
          running.
        {:else}
          Loading…
        {/if}
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
        {#each [["attention", "Attention"], ["recent", "Recent"], ["name", "Name"]] as [value, label] (value)}
          <button
            class="px-3 py-1.5 text-xs transition-colors first:rounded-l-md last:rounded-r-md
                   {sort === value ? 'bg-secondary text-secondary-foreground' : 'text-muted-foreground hover:text-foreground'}"
            onclick={() => (sort = value as typeof sort)}
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
    <p class="rounded-md border border-red-500/40 bg-red-500/10 px-4 py-2.5 text-sm text-red-500 dark:text-red-300">
      {error}
    </p>
  {/if}

  {#if stats.length > 0}
    <div class="flex flex-wrap gap-2">
      {#each stats as stat (stat.key)}
        <button
          type="button"
          onclick={() => toggleLens(stat.key)}
          aria-pressed={lens === stat.key}
          class="rounded-md border px-3 py-1.5 text-xs transition-colors
                 {lens === stat.key
            ? 'border-foreground/30 bg-secondary'
            : 'border-border hover:border-foreground/25 hover:bg-secondary/40'}"
        >
          <span class="font-mono tabular-nums {toneClass[stat.tone]}">{stat.count}</span>
          <span class="ml-1.5 text-muted-foreground">{stat.label}</span>
        </button>
      {/each}
      {#if lens !== "all"}
        <button
          type="button"
          onclick={() => (lens = "all")}
          class="rounded-md px-2.5 py-1.5 text-xs text-muted-foreground underline-offset-2 hover:underline"
        >
          show all
        </button>
      {/if}
    </div>
  {:else if totals && totals.projects > 0}
    <p class="text-xs text-emerald-600 dark:text-emerald-400">
      Everything is running, healthy, and matches what is on disk.
    </p>
  {/if}

  {#if loading && projects.length === 0}
    <p class="text-sm text-muted-foreground">Loading…</p>
  {:else if shown.length === 0}
    <Empty
      title={projects.length === 0
        ? "No projects discovered yet."
        : "Nothing matches that filter."}
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
            class="flex h-full flex-col rounded-lg border p-4 transition-colors hover:bg-secondary/30
                   {project.attention && !project.archived
              ? 'border-amber-500/40 hover:border-amber-500/60'
              : 'border-border hover:border-foreground/25'}"
          >
            <div class="flex items-baseline gap-2">
              <span class="min-w-0 truncate font-medium" title={project.name}>{project.name}</span>
              {#if project.archived}
                <span class="shrink-0 rounded bg-secondary px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
                  archived
                </span>
              {/if}
            </div>

            <!-- The state line. Each failure mode gets its own badge and its
                 own colour: an unhealthy container is running and answering
                 wrongly, a crashed one is not running, and a container someone
                 stopped is neither. One red count for all three said nothing.
                 Only non-zero badges are drawn — a row of zeroes is noise that
                 hides the one card that is not fine. -->
            <div class="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
              {#if project.services === 0}
                <span class="text-muted-foreground">no containers observed</span>
              {:else}
                <span
                  class="mr-1 font-mono tabular-nums {project.running === project.services
                    ? 'text-emerald-600 dark:text-emerald-400'
                    : 'text-muted-foreground'}"
                  title="{project.running} of {project.services} containers running"
                >
                  {project.running}/{project.services}
                </span>
              {/if}
              {#each badges(project) as badge (badge.label)}
                <span class="rounded px-1.5 py-0.5 text-[11px] {badge.class}" title={badge.title}>
                  {badge.label}
                </span>
              {/each}
            </div>

            {#if project.working_dir}
              <p class="mt-2 truncate font-mono text-[11px] text-muted-foreground/70" title={project.working_dir}>
                {project.working_dir}
              </p>
            {/if}

            <p class="mt-auto pt-3 text-xs text-muted-foreground">
              {#if project.last_changed_at}
                changed <Timestamp ts={project.last_changed_at} />
              {:else}
                last seen <Timestamp ts={project.last_seen_at} />
              {/if}
            </p>
          </a>
        </li>
      {/each}
    </ul>
  {/if}
</div>
