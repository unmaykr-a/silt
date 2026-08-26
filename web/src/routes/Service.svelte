<script lang="ts">
  import { api, type ServiceHistory } from "$lib/api/client";
  import { link } from "$lib/router.svelte";
  import Timestamp from "$lib/components/Timestamp.svelte";
  import Empty from "$lib/components/Empty.svelte";
  import { shortDigest } from "$lib/format";

  let { projectId, service }: { projectId: number; service: string } = $props();

  let history = $state<ServiceHistory | null>(null);
  let error = $state<string | null>(null);

  $effect(() => {
    const key = [projectId, service];
    void key;
    const controller = new AbortController();
    api
      .serviceHistory(projectId, service, controller.signal)
      .then((h) => {
        history = h;
        error = null;
      })
      .catch((err: Error) => {
        if (err.name !== "AbortError") error = err.message;
      });
    return () => controller.abort();
  });

  // Successive observations where the image identity actually changed. This is
  // the question the screen exists to answer: when did this image change, and
  // to what?
  const imageHistory = $derived.by(() => {
    const obs = history?.observations ?? [];
    const out: typeof obs = [];
    let lastID = "";
    // Observations arrive newest first; walk back so "first seen" reads right.
    for (let i = obs.length - 1; i >= 0; i--) {
      if (obs[i].image_id !== lastID) {
        lastID = obs[i].image_id ?? "";
        out.unshift(obs[i]);
      }
    }
    return out;
  });

  const restarts = $derived(history?.observations?.map((o) => o.restart_count ?? 0).reverse() ?? []);
  const peakRestarts = $derived(restarts.length === 0 ? 0 : Math.max(...restarts));
</script>

{#if error}
  <p class="rounded border border-red-900/60 bg-red-950/40 px-4 py-3 text-sm text-red-300">{error}</p>
{/if}

<div class="space-y-8">
  <header>
    <a use:link href="/projects/{projectId}" class="text-xs text-muted-foreground underline-offset-4 hover:underline">
      ← back to project
    </a>
    <h2 class="mt-1 text-2xl font-semibold tracking-tight">{service}</h2>
  </header>

  <section>
    <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Image history</h3>
    {#if imageHistory.length === 0}
      <div class="mt-3"><Empty title="No image history recorded yet." /></div>
    {:else}
      <ul class="mt-3 divide-y divide-border border-y border-border">
        {#each imageHistory as obs (obs.snapshot_id)}
          <li class="flex flex-wrap items-baseline gap-3 py-2 text-sm">
            <span class="font-mono text-xs">{obs.image_ref}</span>
            <span
              class="font-mono text-xs text-muted-foreground"
              title={obs.image_digest || obs.image_id}
            >
              {shortDigest(obs.image_digest || obs.image_id)}
            </span>
            {#if !obs.image_digest}
              <span class="text-xs text-muted-foreground/60" title="Locally built images have no registry digest">
                local build
              </span>
            {/if}
            <Timestamp ts={obs.taken_at} class="ml-auto text-xs text-muted-foreground" />
          </li>
        {/each}
      </ul>
    {/if}
  </section>

  <section>
    <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Restarts</h3>
    {#if restarts.length === 0}
      <p class="mt-3 text-sm text-muted-foreground">No observations yet.</p>
    {:else if peakRestarts === 0}
      <!-- A sparkline of all zeroes is an invisible flat line that reads as a
           broken chart. Say what it means instead. -->
      <p class="mt-3 text-sm text-muted-foreground">
        No restarts across {restarts.length}
        {restarts.length === 1 ? "observation" : "observations"}.
      </p>
    {:else}
      <div class="mt-3 flex h-12 items-end gap-px" aria-label="restart count over time">
        {#each restarts as count, i (i)}
          <div
            class="flex-1 rounded-sm {count > 0 ? 'bg-amber-500/70' : 'bg-border'}"
            style="height: {count > 0 ? Math.max(8, (count / peakRestarts) * 100) : 4}%"
            title="{count} restarts"
          ></div>
        {/each}
      </div>
      <p class="mt-1 text-xs text-muted-foreground">peak {peakRestarts}</p>
    {/if}
  </section>

  <section>
    <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">
      Environment key history
    </h3>
    <p class="mt-1 text-xs text-muted-foreground/70">
      Redacted values are shown as keyed digests. A changed digest proves the value changed;
      the value itself was never stored.
    </p>
    {#if (history?.env_changes ?? []).length === 0}
      <div class="mt-3"><Empty title="No environment changes recorded." /></div>
    {:else}
      <ul class="mt-3 divide-y divide-border border-y border-border">
        {#each history!.env_changes as change (change.key + change.taken_at)}
          <li class="flex flex-wrap items-baseline gap-3 py-2 text-sm">
            <span class="font-mono text-xs">{change.key}</span>
            {#if change.redacted}
              <span class="font-mono text-xs text-muted-foreground" title="Keyed digest, comparable only within this install">
                {change.digest}
              </span>
              <span class="text-xs text-muted-foreground/60">{change.value_len_bucket}</span>
            {:else}
              <span class="font-mono text-xs text-emerald-300/80">{change.value}</span>
            {/if}
            <span class="text-xs text-muted-foreground/60">
              {change.first_seen ? "first seen" : "changed"}
            </span>
            <Timestamp ts={change.taken_at} class="ml-auto text-xs text-muted-foreground" />
          </li>
        {/each}
      </ul>
    {/if}
  </section>
</div>
