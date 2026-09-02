<script lang="ts">
  import { api, type AuditLog } from "$lib/api/client";
  import Timestamp from "./Timestamp.svelte";
  import Empty from "./Empty.svelte";

  /**
   * What people did to Silt itself.
   *
   * Lives under Security rather than on a screen of its own: it is the answer
   * to a question you ask about access, and a top-level tab would suggest it
   * is something to read regularly. It is not — it is something to have.
   */
  let log = $state<AuditLog | null>(null);
  let error = $state<string | null>(null);
  let loading = $state(true);
  let limit = $state(50);

  $effect(() => {
    const n = limit;
    const controller = new AbortController();
    loading = true;
    api
      .audit(n, controller.signal)
      .then((l) => {
        log = l;
        error = null;
      })
      .catch((err: Error) => {
        if (err.name !== "AbortError") error = err.message;
      })
      .finally(() => (loading = false));
    return () => controller.abort();
  });

  // Docker's action strings are namespaced and terse. Say them in words.
  const LABELS: Record<string, string> = {
    "auth.sign_in": "Signed in",
    "auth.sign_in_failed": "Sign-in refused",
    "auth.sign_out": "Signed out",
    "auth.password_changed": "Password changed",
    "auth.account_claimed": "Account claimed",
    "auth.account_changed": "Account changed",
    "auth.sessions_revoked": "All sessions revoked",
    "settings.changed": "Settings changed",
    "settings.reset": "Settings reset to the environment",
    "settings.notifications_tested": "Notification test sent",
    "maintenance.prune": "History pruned",
    "maintenance.snapshot": "Snapshot forced",
    "redaction.rule_added": "Redaction rule added",
    "redaction.rule_removed": "Redaction rule removed",
  };

  function label(action: string): string {
    return LABELS[action] ?? action;
  }

  /** The detail, as a short readable line rather than raw JSON. */
  function summarise(detail: Record<string, unknown> | undefined): string {
    if (!detail) return "";
    const parts: string[] = [];
    for (const [key, value] of Object.entries(detail)) {
      if (value === null || value === undefined || value === false) continue;
      if (Array.isArray(value)) {
        if (value.length === 0) continue;
        parts.push(`${key}: ${value.join(", ")}`);
      } else if (typeof value === "object") {
        continue;
      } else {
        parts.push(`${key}: ${value}`);
      }
    }
    return parts.join(" · ");
  }
</script>

<div>
  <div class="flex flex-wrap items-baseline justify-between gap-2">
    <h4 class="text-sm font-medium">Activity</h4>
    {#if log}
      <p class="text-xs text-muted-foreground">
        {log.total}
        {log.total === 1 ? "entry" : "entries"}
      </p>
    {/if}
  </div>
  <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
    Silt records what changed on your host; this is the same question asked about Silt. It keeps
    what changed, never what it changed to — settings hold an ingest token and notification
    targets, and this is a list built to be read.
  </p>

  {#if error}
    <p class="mt-3 text-xs text-red-600 dark:text-red-400">{error}</p>
  {:else if loading && !log}
    <p class="mt-3 text-xs text-muted-foreground">Loading…</p>
  {:else if !log || log.entries.length === 0}
    <div class="mt-3">
      <Empty
        title="Nothing recorded yet."
        hint="Signing in, changing a setting or running a prune all leave an entry."
      />
    </div>
  {:else}
    <ul class="mt-3 divide-y divide-border border-y border-border">
      {#each log.entries as entry (entry.id)}
        <li class="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 py-2 text-xs">
          <span
            class="size-1.5 shrink-0 rounded-full {entry.ok
              ? 'bg-border'
              : 'bg-red-500'}"
            aria-hidden="true"
          ></span>
          <span class={entry.ok ? "" : "text-red-600 dark:text-red-400"}>{label(entry.action)}</span>
          {#if entry.actor}
            <span class="text-muted-foreground">by {entry.actor}</span>
          {/if}
          {#if entry.method && entry.method !== "system"}
            <span class="rounded bg-secondary px-1.5 py-0.5 text-[10px] text-muted-foreground">
              {entry.method}
            </span>
          {/if}
          {#if summarise(entry.detail)}
            <span class="min-w-0 truncate font-mono text-[11px] text-muted-foreground/70">
              {summarise(entry.detail)}
            </span>
          {/if}
          <span class="ml-auto shrink-0 text-muted-foreground">
            {#if entry.remote}
              <span class="mr-2 font-mono text-[11px] text-muted-foreground/60">{entry.remote}</span>
            {/if}
            <Timestamp ts={entry.ts} />
          </span>
        </li>
      {/each}
    </ul>

    {#if log.total > log.entries.length}
      <button
        type="button"
        class="mt-3 rounded-md border border-border px-2.5 py-1.5 text-xs transition-colors hover:bg-secondary/60"
        onclick={() => (limit += 100)}
      >
        Show more
      </button>
    {/if}
  {/if}
</div>
