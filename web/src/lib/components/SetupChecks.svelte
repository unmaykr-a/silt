<script lang="ts">
  import type { SetupCheck } from "$lib/api/client";

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
