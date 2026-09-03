<script lang="ts">
  import { IS_DEMO } from "$lib/demo";

  /**
   * A single strip above the header, in the published demo only.
   *
   * Every write in the demo fails, and someone who does not know they are in
   * a demo reads that as Silt being broken. It says so once, at the top,
   * where the answer arrives before the question — and it can be dismissed,
   * because the sentence is worth reading once and not on every screen.
   *
   * Compiled out of a normal build: IS_DEMO is a build-time constant, so the
   * markup below is dead code Rollup removes.
   */
  let dismissed = $state(false);
</script>

{#if IS_DEMO && !dismissed}
  <div
    class="flex shrink-0 items-center gap-2 border-b border-sky-500/25 bg-sky-500/10 px-3 py-1.5 text-xs text-foreground sm:px-4"
  >
    <span class="size-1.5 shrink-0 rounded-full bg-sky-500" aria-hidden="true"></span>
    <p class="min-w-0">
      <span class="font-medium">This is a read-only demo</span>
      <span class="text-muted-foreground">
        — captured from a real Silt watching a made-up host. Nothing updates and nothing saves.
      </span>
    </p>
    <a
      href="https://github.com/unmaykr-a/silt"
      target="_blank"
      rel="noreferrer noopener"
      class="ml-auto shrink-0 rounded-md border border-sky-500/30 px-2 py-0.5 whitespace-nowrap transition-colors hover:bg-sky-500/15"
    >
      Run it yourself
    </a>
    <button
      type="button"
      class="shrink-0 rounded-md p-1 text-muted-foreground transition-colors hover:text-foreground"
      onclick={() => (dismissed = true)}
      aria-label="Dismiss"
    >
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
        <path d="M18 6 6 18M6 6l12 12" />
      </svg>
    </button>
  </div>
{/if}
