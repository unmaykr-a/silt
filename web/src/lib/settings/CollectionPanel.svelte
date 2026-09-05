<script lang="ts">
  import Field from "./Field.svelte";
  import IntervalField from "./IntervalField.svelte";
  import { input } from "./input";
  import { INTERVALS } from "./intervals";
  import type { SettingsStore } from "./store.svelte";

  let { store }: { store: SettingsStore } = $props();
  const draft = $derived(store.draft);
</script>

<section>
  <h3 class="text-sm font-semibold">Collection</h3>
  <div class="mt-2 divide-y divide-border">
    <IntervalField
      {store}
      name="snapshot_interval_ms"
      label="Reconcile interval"
      envVar="SILT_SNAPSHOT_INTERVAL"
      hint="Silt records changes as Docker reports them; this is the sweep that catches whatever the event stream missed."
      options={INTERVALS}
    />

    <Field
      {store}
      name="keep_keys"
      label="Keys kept readable"
      envVar="SILT_KEEP_KEYS"
      hint="Every environment value is a keyed digest unless its key is on the safe list. These are the extras you added, comma separated; one leading or trailing * is allowed."
    >
      <input id="keep_keys" bind:value={draft.keep_keys} placeholder="PUID, TZ, MY_APP_*" class={input} />
    </Field>

    <Field {store} name="log_level" label="Log level" envVar="SILT_LOG_LEVEL">
      <select id="log_level" bind:value={draft.log_level} class={input}>
        {#each ["debug", "info", "warn", "error"] as level (level)}
          <option value={level}>{level}</option>
        {/each}
      </select>
    </Field>
  </div>
</section>
