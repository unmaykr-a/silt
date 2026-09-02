<script lang="ts">
  import { api, type ServiceHistory, type ServiceObservation } from "$lib/api/client";
  import { link } from "$lib/router.svelte";
  import Timestamp from "$lib/components/Timestamp.svelte";
  import Empty from "$lib/components/Empty.svelte";
  import { shortDigest, datetime, duration } from "$lib/format";
  import { serviceState, stateLegend, type StateKey } from "$lib/servicestate";

  let { projectId, service }: { projectId: number; service: string } = $props();

  let history = $state<ServiceHistory | null>(null);
  let error = $state<string | null>(null);
  let loading = $state(true);

  $effect(() => {
    const key = [projectId, service];
    void key;
    const controller = new AbortController();
    loading = true;
    api
      .serviceHistory(projectId, service, controller.signal)
      .then((h) => {
        history = h;
        error = null;
      })
      .catch((err: Error) => {
        if (err.name !== "AbortError") error = err.message;
      })
      .finally(() => (loading = false));
    return () => controller.abort();
  });

  // Observations arrive newest first.
  const observations = $derived(history?.observations ?? []);
  const current = $derived<ServiceObservation | undefined>(observations[0]);

  // Successive observations where the image identity actually changed. This is
  // the question the screen exists to answer: when did this image change, and
  // to what?
  //
  // Each entry now carries the snapshot before it, so the row can link to the
  // diff that introduced the change rather than leaving you to find it.
  type ImageChange = {
    observation: ServiceObservation;
    previous?: ServiceObservation;
    heldFor?: number;
  };

  const imageHistory = $derived.by<ImageChange[]>(() => {
    const out: ImageChange[] = [];
    let lastID = "";
    let lastEntry: ServiceObservation | undefined;
    // Walk oldest first so "first seen" reads right, then reverse.
    for (let i = observations.length - 1; i >= 0; i--) {
      const obs = observations[i];
      if (obs.image_id !== lastID) {
        out.unshift({ observation: obs, previous: lastEntry });
        lastID = obs.image_id ?? "";
        lastEntry = obs;
      }
    }
    // How long each image was in place, which is the thing you actually want
    // to know when you are asking "how long has this been broken?".
    for (let i = 0; i < out.length; i++) {
      const started = out[i].observation.taken_at;
      const ended = i === 0 ? Date.now() : out[i - 1].observation.taken_at;
      out[i].heldFor = ended - started;
    }
    return out;
  });

  const restarts = $derived(observations.map((o) => o.restart_count ?? 0).reverse());
  const peakRestarts = $derived(restarts.length === 0 ? 0 : Math.max(...restarts));

  // Health and state over the same window as the restarts, so the two read
  // together: a service that restarted four times and is now unhealthy is one
  // story, not two facts on separate rows.
  const stateRun = $derived(observations.map((o) => serviceState(o)).reverse());

  // Colour and wording come from the shared vocabulary rather than from here:
  // this file used to paint an unhealthy container bg-red-500 and a stopped
  // one bg-red-500/70, two shades of one red at the eight pixels a timeline
  // mark gets, which made the strip unreadable for the exact question it is
  // for.
  const currentState = $derived(current ? serviceState(current) : null);

  // Which distinct states this service has actually been in, so the legend
  // explains the marks on screen instead of the full catalogue.
  //
  // Ordered by the shared severity list rather than by when each first
  // appeared: a legend that reshuffles itself between two services is one you
  // have to re-read every time.
  const legendShown = $derived.by(() => {
    const seen = new Map<StateKey, ReturnType<typeof serviceState>>();
    for (const o of observations) {
      const s = serviceState(o);
      if (!seen.has(s.key)) seen.set(s.key, s);
    }
    const order = stateLegend.map((e) => e.key);
    return [...seen.values()].sort((a, b) => order.indexOf(a.key) - order.indexOf(b.key));
  });

  const envChanges = $derived(history?.env_changes ?? []);
</script>

<div class="space-y-6">
  <header>
    <a use:link href="/projects/{projectId}" class="text-xs text-muted-foreground underline-offset-4 hover:underline">
      ← back to project
    </a>
    <div class="mt-1 flex flex-wrap items-baseline gap-3">
      <h2 class="text-2xl font-semibold tracking-tight">{service}</h2>
      {#if current}
        <span class="inline-flex items-center gap-1.5 text-xs" title={currentState?.detail}>
          <span class="size-2 rounded-full {currentState?.dot}"></span>
          <span class={currentState?.text}>{currentState?.label}</span>
        </span>
      {/if}
    </div>
  </header>

  {#if error}
    <p class="rounded-md border border-red-500/40 bg-red-500/10 px-4 py-2.5 text-sm text-red-500 dark:text-red-300">
      {error}
    </p>
  {:else if loading && !history}
    <p class="text-sm text-muted-foreground">Loading…</p>
  {:else if observations.length === 0}
    <Empty
      title="Nothing recorded for this service yet."
      hint="Silt records a service the first time it observes a container carrying its Compose labels."
    />
  {:else}
    <!-- What it is right now, in one row, because that is what most visits
         want before any history. -->
    {#if current}
      <dl class="grid grid-cols-2 gap-px overflow-hidden rounded-md border border-border bg-border sm:grid-cols-4">
        <div class="bg-background p-3">
          <dt class="text-[11px] text-muted-foreground">Image</dt>
          <dd class="mt-0.5 truncate font-mono text-xs" title={current.image_ref}>{current.image_ref || "—"}</dd>
        </div>
        <div class="bg-background p-3">
          <!-- A registry digest and a local image ID are different claims: one
               identifies the image anywhere, the other only on this host. Say
               which of the two is on screen rather than calling both "digest". -->
          <dt class="text-[11px] text-muted-foreground">{current.image_digest ? "Digest" : "Image ID"}</dt>
          <dd class="mt-0.5 font-mono text-xs" title={current.image_digest || current.image_id}>
            {shortDigest(current.image_digest || current.image_id) || "—"}
          </dd>
        </div>
        <div class="bg-background p-3">
          <dt class="text-[11px] text-muted-foreground">Restarts</dt>
          <dd class="mt-0.5 font-mono text-xs tabular-nums {current.restart_count ? 'text-amber-600 dark:text-amber-400' : ''}">
            {current.restart_count ?? 0}
          </dd>
        </div>
        <div class="bg-background p-3">
          <dt class="text-[11px] text-muted-foreground">Last observed</dt>
          <dd class="mt-0.5 text-xs"><Timestamp ts={current.taken_at} /></dd>
        </div>
      </dl>
    {/if}

    <section>
      <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Image history</h3>
      {#if imageHistory.length === 0}
        <div class="mt-3"><Empty title="No image history recorded yet." /></div>
      {:else}
        <div class="mt-2">
          {#each imageHistory as entry, i (entry.observation.snapshot_id)}
            {@const obs = entry.observation}
            <div class="flex flex-wrap items-baseline gap-x-3 gap-y-1 border-b border-border/60 py-2.5 text-sm">
              <span class="w-1 shrink-0 self-stretch rounded-sm {i === 0 ? 'bg-emerald-500' : 'bg-border'}"></span>
              <span class="font-mono text-xs">{obs.image_ref}</span>
              <span class="font-mono text-xs text-muted-foreground" title={obs.image_digest || obs.image_id}>
                {shortDigest(obs.image_digest || obs.image_id)}
              </span>
              {#if !obs.image_digest}
                <span
                  class="text-[11px] text-muted-foreground/60"
                  title="Locally built images have no registry digest"
                >
                  local build
                </span>
              {/if}
              {#if entry.heldFor && entry.heldFor > 60_000}
                <span class="text-[11px] text-muted-foreground/60">
                  {i === 0 ? "for" : "ran for"} {duration(entry.heldFor)}
                </span>
              {/if}

              <span class="ml-auto flex shrink-0 items-center gap-3">
                {#if entry.previous}
                  <!-- The diff that introduced this image. Finding it by hand
                       meant going back to the project and matching timestamps. -->
                  <a
                    use:link
                    href="/diff?from={entry.previous.snapshot_id}&to={obs.snapshot_id}&project={projectId}"
                    class="text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
                  >
                    what changed
                  </a>
                {:else}
                  <span class="text-[11px] text-muted-foreground/50">first seen</span>
                {/if}
                <Timestamp ts={obs.taken_at} class="text-xs text-muted-foreground" />
              </span>
            </div>
          {/each}
        </div>
      {/if}
    </section>

    <section>
      <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">
        State and restarts
      </h3>
      {#if peakRestarts === 0}
        <!-- A sparkline of all zeroes is an invisible flat line that reads as a
             broken chart. Say what it means instead. -->
        <p class="mt-2 text-sm text-muted-foreground">
          No restarts across {restarts.length}
          {restarts.length === 1 ? "observation" : "observations"}.
        </p>
      {:else}
        <div class="mt-2 flex h-14 items-end gap-px" aria-label="restart count over time">
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

      <!-- The same window as a state strip, so restarts and health read as one
           story rather than two facts on separate rows. -->
      <div class="mt-2 flex h-2 gap-px overflow-hidden rounded-sm" aria-label="state over time">
        {#each stateRun as point, i (i)}
          <div class="flex-1 {point.dot}" title="{point.label} — {point.detail}"></div>
        {/each}
      </div>
      <!-- A legend of only the states this service has actually been in. A
           strip of coloured marks with no key is decoration, and the full
           catalogue for a service that has only ever run is noise. -->
      <div class="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1">
        {#each legendShown as entry (entry.key)}
          <span class="inline-flex items-center gap-1.5 text-[11px] text-muted-foreground" title={entry.detail}>
            <span class="size-1.5 rounded-full {entry.dot}"></span>
            {entry.label}
          </span>
        {/each}
        <span class="text-[11px] text-muted-foreground/60">oldest to newest, one mark per observation</span>
      </div>
    </section>

    <section>
      <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">
        Environment key history
      </h3>
      <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground/70">
        Redacted values are shown as keyed digests. A changed digest proves the value changed; the
        value itself was never stored.
      </p>
      {#if envChanges.length === 0}
        <div class="mt-3"><Empty title="No environment changes recorded." /></div>
      {:else}
        <div class="mt-2">
          {#each envChanges as change (change.key + change.taken_at)}
            <div class="flex flex-wrap items-baseline gap-x-3 gap-y-1 border-b border-border/60 py-2 text-sm">
              <span class="w-1 shrink-0 self-stretch rounded-sm {change.first_seen ? 'bg-border' : 'bg-sky-500'}"></span>
              <span class="font-mono text-xs">{change.key}</span>
              {#if change.redacted}
                <span
                  class="font-mono text-xs text-muted-foreground"
                  title="Keyed digest, comparable only within this install"
                >
                  {change.digest}
                </span>
                <span class="text-[11px] text-muted-foreground/60">{change.value_len_bucket}</span>
              {:else}
                <span class="break-all font-mono text-xs text-emerald-600 dark:text-emerald-300/90">
                  {change.value}
                </span>
              {/if}
              <span class="ml-auto flex shrink-0 items-center gap-3">
                <span class="text-[11px] text-muted-foreground/60">
                  {change.first_seen ? "first seen" : "changed"}
                </span>
                <span class="text-xs text-muted-foreground" title={datetime(change.taken_at, { seconds: true })}>
                  <Timestamp ts={change.taken_at} />
                </span>
              </span>
            </div>
          {/each}
        </div>
      {/if}
    </section>
  {/if}
</div>
