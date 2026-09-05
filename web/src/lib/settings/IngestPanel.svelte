<script lang="ts">
  import Field from "./Field.svelte";
  import { input } from "./input";
  import { api } from "$lib/api/client";
  import type { SettingsStore } from "./store.svelte";

  let { store }: { store: SettingsStore } = $props();
  const effective = $derived(store.settings?.effective);
  const rate = $derived(effective?.ingest_rate_per_minute ?? 0);
</script>

<section>
  <h3 class="text-sm font-semibold">Ingest webhook</h3>
  <div class="mt-2 divide-y divide-border">
    <Field
      {store}
      name="ingest_token"
      label="Token"
      envVar="SILT_INGEST_TOKEN"
      hint={effective?.ingest_configured
        ? "Guards POST /api/ingest, and is configured. Typing here replaces it."
        : "Guards POST /api/ingest. Not configured, so the endpoint refuses every request."}
    >
      <input
        id="ingest_token"
        type="password"
        autocomplete="new-password"
        bind:value={store.ingestToken}
        placeholder={effective?.ingest_configured ? "•••••••• — type to replace" : "not configured"}
        class="{input} font-mono text-xs"
      />
      {#if effective?.ingest_configured}
        <button
          type="button"
          class="mt-2 text-[11px] text-muted-foreground underline underline-offset-2 hover:text-foreground"
          onclick={() => store.apply(() => api.updateSettings({ ingest_token: "" }))}
          disabled={store.saving}
        >
          Turn the ingest endpoint off
        </button>
      {/if}
    </Field>

    <!-- Read-only: this is a limit protecting the endpoint, and a UI that could
         raise it from inside would be a way around it. -->
    <Field
      {store}
      name="ingest_rate"
      label="Events per minute"
      envVar="SILT_INGEST_RATE_PER_MINUTE"
      hint="Per source address, applied after the token. A webhook token lives in every config that calls it, so this is the blast radius when one leaks. Over the limit, Silt answers 429 with a Retry-After."
    >
      <span class="font-mono text-xs">{rate > 0 ? `${rate} per minute` : "unlimited"}</span>
    </Field>
  </div>
</section>
