<script lang="ts">
  /**
   * A retention window in days.
   *
   * There were four of these written out longhand, identical but for which
   * draft field they bound — forty lines to say "a number and the word days"
   * four times.
   */
  import Field from "./Field.svelte";
  import { input } from "./input";
  import type { SettingsStore } from "./store.svelte";
  import type { Draft } from "./patch";

  let {
    store,
    name,
    label,
    envVar,
    hint,
  }: {
    store: SettingsStore;
    /** A numeric key of the draft, so a typo is a compile error. */
    name: "retention_days" | "unchanged_retention_days" | "event_retention_days" | "audit_retention_days";
    label: string;
    envVar: string;
    hint?: string;
  } = $props();

  // A local alias so bind: has something concrete to write through.
  const draft: Draft = $derived(store.draft);
</script>

<Field {store} {name} {label} {envVar} {hint}>
  <div class="flex items-center gap-2">
    <input id={name} type="number" min="0" bind:value={draft[name]} class={input} />
    <span class="shrink-0 text-xs text-muted-foreground">days</span>
  </div>
</Field>
