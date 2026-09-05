<script lang="ts">
  /**
   * A duration chosen from a short list.
   *
   * The extra option at the end is the point: a value set in the environment
   * that is not on the list — 90s, say — would otherwise make the select show
   * the wrong entry, and saving the form would silently change a setting nobody
   * touched.
   */
  import Field from "./Field.svelte";
  import { input } from "./input";
  import { duration } from "$lib/format";
  import type { SettingsStore } from "./store.svelte";
  import type { Draft } from "./patch";

  let {
    store,
    name,
    label,
    envVar,
    hint,
    options,
  }: {
    store: SettingsStore;
    name: "snapshot_interval_ms" | "retention_interval_ms" | "vacuum_interval_ms";
    label: string;
    envVar: string;
    hint?: string;
    options: readonly (readonly [string, number])[];
  } = $props();

  const draft: Draft = $derived(store.draft);
  const listed = $derived(options.some(([, ms]) => ms === draft[name]));
</script>

<Field {store} {name} {label} {envVar} {hint}>
  <select id={name} bind:value={draft[name]} class={input}>
    {#each options as [optionLabel, ms] (ms)}
      <option value={ms}>{optionLabel}</option>
    {/each}
    {#if !listed}
      <option value={draft[name]}>{duration(draft[name])}</option>
    {/if}
  </select>
</Field>
