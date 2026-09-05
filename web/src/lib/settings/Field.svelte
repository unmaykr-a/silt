<script lang="ts">
  /**
   * One editable setting: what it is called, where its value came from, and a
   * way back to the environment.
   *
   * The "set here" badge and the reset link are the whole reason this exists as
   * a shared component rather than markup in each panel — the environment is
   * the baseline, so every field has to be able to say whether it is still
   * tracking it, and that answer has to look identical in all nine sections.
   */
  import type { Snippet } from "svelte";
  import type { SettingsStore } from "./store.svelte";

  let {
    store,
    name,
    label,
    envVar,
    hint,
    children,
  }: {
    store: SettingsStore;
    /** Matches the API field name, which is also its key in the search index. */
    name: string;
    label: string;
    envVar: string;
    hint?: string;
    children: Snippet;
  } = $props();

  const overridden = $derived(store.overridden.has(name));
</script>

<div class="grid gap-1.5 py-3.5 sm:grid-cols-[15rem_1fr] sm:gap-6">
  <div class="min-w-0">
    <div class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
      <label for={name} class="text-sm font-medium">{label}</label>
      {#if overridden}
        <span class="rounded bg-secondary px-1 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
          set here
        </span>
      {/if}
    </div>
    {#if hint}
      <p class="mt-1 text-xs leading-relaxed text-muted-foreground/70">{hint}</p>
    {/if}
    <p class="mt-1 font-mono text-[10px] text-muted-foreground/40">{envVar}</p>
    {#if overridden}
      <button
        type="button"
        class="mt-1 text-[11px] text-muted-foreground underline underline-offset-2 hover:text-foreground"
        onclick={() => store.useEnvironment(name)}
        disabled={store.saving || store.readOnly}
      >
        use the environment value
      </button>
    {/if}
  </div>
  <div class="min-w-0 max-w-md">{@render children()}</div>
</div>
