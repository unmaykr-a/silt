<script lang="ts">
  import { api, subscribe, type Project, type Event } from "./lib/api/client";

  type Status = "connecting" | "live" | "offline";

  let status = $state<Status>("connecting");
  let projects = $state<Project[]>([]);
  let events = $state<Event[]>([]);
  let error = $state<string | null>(null);

  async function load(signal: AbortSignal) {
    try {
      const [p, e] = await Promise.all([api.projects(signal), api.events(10, signal)]);
      projects = p;
      events = e;
      error = null;
    } catch (err) {
      if ((err as Error).name === "AbortError") return;
      error = (err as Error).message;
    }
  }

  $effect(() => {
    const controller = new AbortController();
    void load(controller.signal);

    // Live updates: re-read on any change rather than patching state locally,
    // which keeps the page correct even if a frame is dropped for a slow client.
    const unsubscribe = subscribe({
      ready: () => (status = "live"),
      event: () => void load(controller.signal),
      "snapshot.changed": () => void load(controller.signal),
    });

    return () => {
      controller.abort();
      unsubscribe();
      status = "offline";
    };
  });

  const dotClass = $derived(
    status === "live" ? "bg-emerald-400" : status === "connecting" ? "bg-zinc-500" : "bg-red-400",
  );

  const serviceCount = $derived(projects.length);

  function severityClass(severity: string): string {
    if (severity === "error") return "text-red-400";
    if (severity === "warn") return "text-amber-400";
    return "text-zinc-500";
  }

  function relative(ts: number): string {
    const seconds = Math.round((Date.now() - ts) / 1000);
    if (seconds < 60) return `${seconds}s ago`;
    if (seconds < 3600) return `${Math.round(seconds / 60)}m ago`;
    if (seconds < 86400) return `${Math.round(seconds / 3600)}h ago`;
    return `${Math.round(seconds / 86400)}d ago`;
  }
</script>

<main class="min-h-screen bg-zinc-950 px-6 py-16 text-zinc-100">
  <div class="mx-auto max-w-2xl">
    <header class="flex items-baseline justify-between">
      <div>
        <h1 class="text-4xl font-semibold tracking-tight">Silt</h1>
        <p class="mt-1 text-sm text-zinc-500">What settled on your stack, and when.</p>
      </div>
      <div class="flex items-center gap-2 text-xs text-zinc-500">
        <span class="size-2 rounded-full {dotClass}" aria-hidden="true"></span>
        <span>{status}</span>
      </div>
    </header>

    {#if error}
      <p class="mt-8 rounded border border-red-900/60 bg-red-950/40 px-4 py-3 text-sm text-red-300">
        {error}
      </p>
    {/if}

    <section class="mt-10">
      <h2 class="text-xs font-medium uppercase tracking-wide text-zinc-500">
        Projects ({serviceCount})
      </h2>
      {#if projects.length === 0}
        <p class="mt-3 text-sm text-zinc-600">
          No Compose projects discovered yet.
        </p>
      {:else}
        <ul class="mt-3 divide-y divide-zinc-900 border-y border-zinc-900">
          {#each projects as project (project.id)}
            <li class="flex items-baseline justify-between py-3">
              <span class="font-medium">{project.name}</span>
              <span class="font-mono text-xs text-zinc-600">{project.working_dir ?? ""}</span>
            </li>
          {/each}
        </ul>
      {/if}
    </section>

    <section class="mt-10">
      <h2 class="text-xs font-medium uppercase tracking-wide text-zinc-500">Recent events</h2>
      {#if events.length === 0}
        <p class="mt-3 text-sm text-zinc-600">Nothing recorded yet.</p>
      {:else}
        <ul class="mt-3 space-y-2">
          {#each events as event (event.id)}
            <li class="flex items-baseline gap-3 text-sm">
              <span class="font-mono text-xs {severityClass(event.severity)}">{event.type}</span>
              <span class="text-zinc-400">{event.service ?? ""}</span>
              <span class="ml-auto text-xs text-zinc-600" title={new Date(event.ts).toISOString()}>
                {relative(event.ts)}
              </span>
            </li>
          {/each}
        </ul>
      {/if}
    </section>

    <p class="mt-12 text-xs text-zinc-700">
      Timeline, diffs and per-service history arrive in M5.
    </p>
  </div>
</main>
