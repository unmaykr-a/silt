<script lang="ts">
  import { api, type SetupCheck, type Probes } from "$lib/api/client";
  import { Button } from "$lib/components/ui/button";
  import Timestamp from "./Timestamp.svelte";

  /**
   * What is worth knowing about this configuration.
   *
   * Silt is thirty-odd environment variables, most of which do something
   * sensible when unset. That is the right default and it hides a specific
   * failure: a setting that is almost right produces no error at startup and no
   * symptom until the day it matters. Forward auth trusted with no proxy list.
   * Notifications with no base URL, so every message links nowhere.
   *
   * Each of those was discoverable only by reading the whole environment and
   * knowing what to look for. This is that reading, done once. Findings are
   * advice, not validation — anything actually wrong refuses to start.
   *
   * Errors and warnings lead; the notes collapse, because "compose capture is
   * off" is worth being able to find and not worth a panel every visit.
   */
  let { checks }: { checks: SetupCheck[] } = $props();

  // The checks read the configuration. These test it — and the difference
  // matters for the one failure that looks identical to a working install from
  // every other screen: a compose root configured and never mounted renders
  // exactly like a project with no files.
  //
  // On demand rather than on load: each probe touches the network or the
  // filesystem, and a settings screen that hits the Docker socket every time
  // it opens is one nobody should open during an incident.
  let probes = $state<Probes | null>(null);
  let probing = $state(false);
  let probeError = $state<string | null>(null);

  async function runProbes() {
    probing = true;
    probeError = null;
    try {
      probes = await api.probes();
    } catch (err) {
      probeError = (err as Error).message;
    } finally {
      probing = false;
    }
  }

  const attention = $derived(checks.filter((c) => c.level !== "info"));
  const notes = $derived(checks.filter((c) => c.level === "info"));
  let notesOpen = $state(false);

  const tone = (level: string) =>
    level === "error"
      ? "border-red-500/30 bg-red-500/5"
      : level === "warn"
        ? "border-amber-500/30 bg-amber-500/5"
        : "border-border";

  const dot = (level: string) =>
    level === "error" ? "bg-red-500" : level === "warn" ? "bg-amber-500" : "bg-muted-foreground/40";
</script>

{#snippet finding(check: SetupCheck)}
  <div class="rounded-lg border px-3.5 py-3 {tone(check.level)}">
    <div class="flex items-baseline gap-2">
      <span class="mt-1.5 size-1.5 shrink-0 self-start rounded-full {dot(check.level)}" aria-hidden="true"></span>
      <div class="min-w-0">
        <p class="text-sm font-medium">{check.title}</p>
        <p class="mt-1 text-xs leading-relaxed text-muted-foreground">{check.detail}</p>
        {#if check.env_vars?.length}
          <!-- The variable is what you go and change, so it is here rather
               than left for the reader to work out. -->
          <p class="mt-1.5 flex flex-wrap gap-x-2 gap-y-1 font-mono text-[10px] text-muted-foreground/50">
            {#each check.env_vars as v (v)}<span>{v}</span>{/each}
          </p>
        {/if}
      </div>
    </div>
  </div>
{/snippet}

<section>
  <h3 class="text-sm font-semibold">Setup</h3>
  <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
    Settings that are legal, working, and probably not what you meant. Anything actually wrong
    refuses to start, so nothing here is an outage — but the two at the top are how an install ends
    up readable by people who should not be reading it.
  </p>

  <div class="mt-3 space-y-2">
    {#if attention.length === 0}
      <div class="rounded-lg border border-emerald-500/30 bg-emerald-500/5 px-3.5 py-3">
        <div class="flex items-baseline gap-2">
          <span class="mt-1.5 size-1.5 shrink-0 self-start rounded-full bg-emerald-500" aria-hidden="true"></span>
          <p class="text-sm">Nothing to flag. Authentication, notifications and retention all look deliberate.</p>
        </div>
      </div>
    {:else}
      {#each attention as check (check.id)}
        {@render finding(check)}
      {/each}
    {/if}

    {#if notes.length > 0}
      <button
        type="button"
        class="flex items-center gap-1.5 pt-1 text-xs text-muted-foreground transition-colors hover:text-foreground"
        onclick={() => (notesOpen = !notesOpen)}
        aria-expanded={notesOpen}
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"
             class="transition-transform duration-200 {notesOpen ? 'rotate-90' : ''}" aria-hidden="true">
          <path d="m9 18 6-6-6-6" />
        </svg>
        {notes.length} thing{notes.length === 1 ? "" : "s"} Silt could also be doing
      </button>
      {#if notesOpen}
        <div class="space-y-2">
          {#each notes as check (check.id)}
            {@render finding(check)}
          {/each}
        </div>
      {/if}
    {/if}
  </div>
</section>

<section class="mt-8">
  <div class="flex flex-wrap items-baseline justify-between gap-3">
    <div>
      <h3 class="text-sm font-semibold">Does it work?</h3>
      <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
        The review above reads your configuration. This asks it: does the Docker endpoint
        answer, is the database readable, is each compose root actually mounted? A root you
        configured and never mounted looks exactly like a project with no files from every
        other screen.
      </p>
    </div>
    <Button variant="outline" size="sm" onclick={runProbes} disabled={probing}>
      {probing ? "Checking…" : probes ? "Check again" : "Run checks"}
    </Button>
  </div>

  {#if probeError}
    <p class="mt-3 rounded-md border border-red-500/40 bg-red-500/10 px-4 py-2.5 text-sm text-red-500 dark:text-red-300">
      {probeError}
    </p>
  {/if}

  {#if probes}
    <dl class="mt-3 divide-y divide-border rounded-lg border border-border">
      {#each probes.probes as probe (probe.id)}
        <div class="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-3.5 py-2.5">
          <span
            class="mt-1.5 size-1.5 shrink-0 self-start rounded-full {probe.ok ? 'bg-emerald-500' : 'bg-red-500'}"
            aria-hidden="true"
          ></span>
          <dt class="text-sm">{probe.label}</dt>
          <dd class="min-w-0 flex-1 font-mono text-xs {probe.ok ? 'text-muted-foreground' : 'text-red-500 dark:text-red-300'}">
            {probe.detail}
          </dd>
          <!-- Its own signal: an endpoint answering in four seconds is
               working, and worth knowing about. -->
          <dd class="shrink-0 text-[11px] tabular-nums text-muted-foreground/50">{probe.took_ms} ms</dd>
        </div>
      {/each}
    </dl>
    <p class="mt-2 text-[11px] text-muted-foreground/60">
      Checked <Timestamp ts={probes.checked_at} />.
    </p>
  {/if}
</section>
