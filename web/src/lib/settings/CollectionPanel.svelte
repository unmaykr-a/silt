<script lang="ts">
  import Field from "./Field.svelte";
  import IntervalField from "./IntervalField.svelte";
  import { input } from "./input";
  import { INTERVALS } from "./intervals";
  import { bytes } from "$lib/format";
  import type { SettingsStore } from "./store.svelte";

  let { store }: { store: SettingsStore } = $props();
  const draft = $derived(store.draft);
</script>

<section>
  <h3 class="text-sm font-semibold">Collection</h3>
  <div class="mt-2 divide-y divide-border">
    <Field
      {store}
      name="host_name"
      label="Host name"
      envVar="SILT_HOST_NAME"
      hint="How this Docker host is labelled. Renaming it moves the existing history rather than starting a second host beside it — unless something already holds the new name, in which case both are left alone and this host joins the existing one."
    >
      <input id="host_name" bind:value={draft.host_name} placeholder="local" class={input} />
    </Field>

    <Field
      {store}
      name="docker_host"
      label="Docker endpoint"
      envVar="SILT_DOCKER_HOST"
      hint="The read-only socket proxy Silt observes through. Saving redials: the event stream to the old engine ends and reconnects to this one. A dial it cannot open is refused and the old endpoint stays."
    >
      <input
        id="docker_host"
        bind:value={draft.docker_host}
        placeholder="tcp://docker-socket-proxy:2375"
        class="{input} font-mono text-xs"
      />
    </Field>

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

    <Field
      {store}
      name="max_compose_file_bytes"
      label="Max compose file"
      envVar="SILT_MAX_COMPOSE_FILE_BYTES"
      hint="A captured compose file larger than this is recorded as too_large rather than stored. Raising it makes an already-skipped file readable on its next capture."
    >
      <div class="flex items-baseline gap-2">
        <input
          id="max_compose_file_bytes"
          type="number"
          min="1024"
          step="1024"
          bind:value={draft.max_compose_file_bytes}
          class="{input} max-w-40"
        />
        <span class="text-xs text-muted-foreground">bytes — {bytes(Number(draft.max_compose_file_bytes) || 0)}</span>
      </div>
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
