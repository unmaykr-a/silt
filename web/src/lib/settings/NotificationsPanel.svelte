<script lang="ts">
  import Field from "./Field.svelte";
  import { input } from "./input";
  import { api, type NotifyTestResults } from "$lib/api/client";
  import type { SettingsStore } from "./store.svelte";

  let { store }: { store: SettingsStore } = $props();
  const draft = $derived(store.draft);
  const targets = $derived(store.settings?.effective.notify_targets ?? []);

  // Sending a test message.
  //
  // A shoutrrr URL is wrong until something tries to send, and the only thing
  // that tries to send is the change that mattered. Without this the first
  // proof that notifications work is the outage they were configured for.
  let results = $state<NotifyTestResults["results"] | null>(null);
  let testing = $state(false);
  let testError = $state<string | null>(null);

  async function sendTest() {
    testing = true;
    results = null;
    testError = null;
    try {
      results = (await api.testNotifications()).results;
    } catch (err) {
      testError = (err as Error).message;
    } finally {
      testing = false;
    }
  }
</script>

<section>
  <h3 class="text-sm font-semibold">Notifications</h3>
  <div class="mt-2 divide-y divide-border">
    <Field
      {store}
      name="notify_urls"
      label="Targets"
      envVar="SILT_NOTIFY_URLS"
      hint="shoutrrr URLs, one per line. A shoutrrr URL carries the credential for the service it points at, so Silt shows what is configured but never hands the URL back. Typing here replaces the whole list."
    >
      {#if targets.length > 0}
        <ul class="mb-2 space-y-1">
          {#each targets as target, i (i)}
            <li class="font-mono text-xs text-muted-foreground">{target}</li>
          {/each}
        </ul>
      {:else}
        <p class="mb-2 font-mono text-xs text-muted-foreground">none configured</p>
      {/if}
      <textarea
        id="notify_urls"
        bind:value={store.notifyUrls}
        rows="3"
        placeholder="gotify://gotify.example.com/AppToken&#10;discord://token@id"
        class="{input} font-mono text-xs"
      ></textarea>

      {#if targets.length > 0}
        <div class="mt-2 flex flex-wrap items-center gap-2">
          <button
            type="button"
            onclick={sendTest}
            disabled={testing}
            class="rounded-md border border-border px-2.5 py-1.5 text-xs transition-colors hover:bg-secondary/60 disabled:opacity-50"
          >
            {testing ? "Sending…" : "Send a test"}
          </button>
          <span class="text-[11px] text-muted-foreground">
            Tests what is saved, not what is typed above.
          </span>
        </div>
      {/if}

      {#if testError}
        <p class="mt-2 text-xs text-red-600 dark:text-red-400">{testError}</p>
      {/if}
      {#if results}
        <ul class="mt-2 space-y-1">
          {#each results as result (result.index)}
            <!-- The reason sits under its target rather than beside it: a
                 provider error is long enough to wrap, and a wrapped one reads
                 as belonging to nothing. -->
            <li class="text-xs">
              <div class="flex items-baseline gap-2">
                <span class={result.ok ? "text-emerald-600 dark:text-emerald-400" : "text-red-600 dark:text-red-400"}>
                  {result.ok ? "sent" : "failed"}
                </span>
                <span class="min-w-0 truncate font-mono text-[11px] text-muted-foreground">{result.target}</span>
              </div>
              {#if result.error}
                <p class="ml-1 border-l border-border pl-2.5 text-[11px] text-muted-foreground">
                  {result.error}
                </p>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </Field>

    <Field
      {store}
      name="notify_on"
      label="Notify on"
      envVar="SILT_NOTIFY_ON"
      hint="Change kinds, comma separated, or `all`."
    >
      <input id="notify_on" bind:value={draft.notify_on} class="{input} font-mono text-xs" />
    </Field>

    <Field
      {store}
      name="notify_min_severity"
      label="Minimum severity"
      envVar="SILT_NOTIFY_MIN_SEVERITY"
      hint="ANDed with the kinds above."
    >
      <select id="notify_min_severity" bind:value={draft.notify_min_severity} class={input}>
        {#each ["low", "medium", "high"] as level (level)}
          <option value={level}>{level}</option>
        {/each}
      </select>
    </Field>

    <Field
      {store}
      name="base_url"
      label="Base URL"
      envVar="SILT_BASE_URL"
      hint="Where Silt is reachable, used to build the link in a notification. Empty omits the link."
    >
      <input id="base_url" bind:value={draft.base_url} placeholder="https://silt.example.lan" class={input} />
    </Field>
  </div>
</section>
