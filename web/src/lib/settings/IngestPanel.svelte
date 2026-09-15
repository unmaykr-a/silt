<script lang="ts">
  import Field from "./Field.svelte";
  import { input } from "./input";
  import { api } from "$lib/api/client";
  import type { SettingsStore } from "./store.svelte";

  let { store }: { store: SettingsStore } = $props();
  const effective = $derived(store.settings?.effective);
  const draft = $derived(store.draft);
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

    <!-- Editable, which it was not until 1.1.0 on the grounds that a UI able to
         raise a limit is a way around it. That never held: the panel directly
         above turns the endpoint off entirely, so anyone who can reach here
         already has the stronger control. The case for editing it is the
         ordinary one — a legitimate sender is being refused and you want it
         working now, not after a container recreate. -->
    <Field
      {store}
      name="ingest_rate_per_minute"
      label="Events per minute"
      envVar="SILT_INGEST_RATE_PER_MINUTE"
      hint="Per source address, applied after the token. A webhook token lives in every config that calls it, so this is the blast radius when one leaks. Over the limit, Silt answers 429 with a Retry-After."
    >
      <div class="flex items-baseline gap-2">
        <input
          id="ingest_rate_per_minute"
          type="number"
          min="0"
          bind:value={draft.ingest_rate_per_minute}
          class="{input} max-w-28"
        />
        <span class="text-xs text-muted-foreground">
          {Number(draft.ingest_rate_per_minute) > 0 ? "per minute, per source" : "0 — no limit"}
        </span>
      </div>
    </Field>
  </div>
</section>
