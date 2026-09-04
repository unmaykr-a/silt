<script lang="ts">
  import {
    api,
    type Settings,
    type SettingsPatch,
    type PruneResult,
    type AuthState,
    type NotifyTestResults,
  } from "$lib/api/client";
  import { bytes, duration, sampleDate } from "$lib/format";
  import { prefs, type Clock, type DateStyle, type Layout, type TimeStamps } from "$lib/prefs.svelte";
  import { Button } from "$lib/components/ui/button";
  import AuditLog from "$lib/components/AuditLog.svelte";
  import SetupChecks from "$lib/components/SetupChecks.svelte";
  import Segmented from "$lib/components/Segmented.svelte";
  import Toggle from "$lib/components/Toggle.svelte";
  import { theme, type Theme } from "$lib/theme.svelte";
  import { SECTIONS, searchSettings, overrideCounts, type SectionID } from "$lib/settingsindex";
  import type { Snippet } from "svelte";

  // The environment is the baseline; anything edited here is stored as an
  // override on top of it. That is why every field can say where its value
  // came from, and why "use the environment value" is a button rather than a
  // matter of typing the old number back in.
  //
  // The sections are a left rail rather than one long scroll: the previous
  // version put a save button and seven headings on a page taller than the
  // window, which is the same complaint as a settings link at the bottom of a
  // thirty-item list.
  let settings = $state<Settings | null>(null);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let saving = $state(false);
  let pruning = $state(false);
  let pruned = $state<PruneResult | null>(null);

  // The rail reads its sections from the search index rather than declaring
  // its own list, so a section can only exist in one place and the two cannot
  // drift into disagreeing about what this screen contains.
  let section = $state<SectionID>("setup");

  // Settings search. Nine sections and forty-odd fields is past the point
  // where "it is in here somewhere" works, and the variable name is what
  // people have in hand — the compose file is where they know these from.
  let query = $state("");
  const hits = $derived(searchSettings(query));
  let searchBox = $state<HTMLInputElement | null>(null);

  // Sections with nothing to save. Listed once rather than as a chain of
  // inequalities that quietly grows a hole every time a section is added.
  const READ_ONLY_SECTIONS = new Set<SectionID>([
    "setup",
    "appearance",
    "security",
    "identity",
    "environment",
    "storage",
  ]);

  function goTo(id: SectionID) {
    section = id;
    query = "";
  }

  // The form's working copy, kept separate from `settings` so a field being
  // typed into is not overwritten by a background refresh.
  type Draft = {
    snapshot_interval_ms: number;
    retention_days: number;
    unchanged_retention_days: number;
    event_retention_days: number;
    audit_retention_days: number;
    retention_interval_ms: number;
    vacuum_interval_ms: number;
    keep_keys: string;
    base_url: string;
    log_level: string;
    notify_on: string;
    notify_min_severity: string;
  };
  let draft = $state<Draft>(emptyDraft());
  // Write-only fields. They are never returned by the API, so they start empty
  // every time and only travel when someone types into them.
  let notifyUrls = $state("");

  // Sending a test message.
  //
  // A shoutrrr URL is wrong until something tries to send, and the only thing
  // that tries to send is the change that mattered. Without this the first
  // proof that notifications work is the outage they were configured for.
  type NotifyTest = { results: NotifyTestResults["results"]; failed: number };
  let notifyTest = $state<NotifyTest | null>(null);
  let notifyTesting = $state(false);
  let notifyTestError = $state<string | null>(null);

  async function sendTestNotification() {
    notifyTesting = true;
    notifyTest = null;
    notifyTestError = null;
    try {
      const out = await api.testNotifications();
      notifyTest = { results: out.results, failed: out.failed };
    } catch (err) {
      notifyTestError = (err as Error).message;
    } finally {
      notifyTesting = false;
    }
  }
  let ingestToken = $state("");

  // A non-null starting value rather than null: every control below lives in a
  // snippet, and a snippet is a hoisted function that no {#if} can narrow
  // into. Rendering is gated on `settings` instead.
  function emptyDraft(): Draft {
    return {
      snapshot_interval_ms: 300_000,
      retention_days: 365,
      unchanged_retention_days: 7,
      event_retention_days: 90,
      audit_retention_days: 730,
      retention_interval_ms: 3_600_000,
      vacuum_interval_ms: 0,
      keep_keys: "",
      base_url: "",
      log_level: "info",
      notify_on: "",
      notify_min_severity: "medium",
    };
  }

  function toDraft(s: Settings): Draft {
    const e = s.effective;
    return {
      snapshot_interval_ms: e.snapshot_interval_ms,
      retention_days: e.retention_days,
      unchanged_retention_days: e.unchanged_retention_days,
      event_retention_days: e.event_retention_days,
      audit_retention_days: e.audit_retention_days,
      retention_interval_ms: e.retention_interval_ms,
      vacuum_interval_ms: e.vacuum_interval_ms,
      keep_keys: e.keep_keys.join(", "),
      base_url: e.base_url,
      log_level: e.log_level,
      notify_on: e.notify_on.join(", "),
      notify_min_severity: e.notify_min_severity,
    };
  }

  function adopt(s: Settings) {
    settings = s;
    draft = toDraft(s);
    notifyUrls = "";
    ingestToken = "";
  }

  $effect(() => {
    const controller = new AbortController();
    api
      .settings(controller.signal)
      .then((s) => {
        adopt(s);
        error = null;
      })
      .catch((err: Error) => {
        if (err.name !== "AbortError") error = err.message;
      });
    return () => controller.abort();
  });

  const overridden = $derived(new Set(settings?.overridden ?? []));

  // What this identity may do. A viewer reads every screen and changes
  // nothing, so the controls that would be refused are not offered at all: a
  // save button that always fails is worse than no save button.
  //
  // Its own request rather than the Security section's, which only loads when
  // that section is open — the role is needed from the first render.
  let role = $state<string>("admin");
  const readOnly = $derived(role === "viewer");

  $effect(() => {
    const controller = new AbortController();
    api
      .authState(controller.signal)
      .then((a) => (role = a.role ?? "admin"))
      .catch(() => {});
    return () => controller.abort();
  });
  const counts = $derived(overrideCounts(overridden));
  // Errors and warnings from the setup review, badged on the rail so an
  // install nobody has authenticated says so from whichever section you open.
  const attention = $derived((settings?.checks ?? []).filter((c) => c.level !== "info").length);

  // Security is read-only here on purpose: every knob on it is the boundary
  // protecting this screen. What it can do is say what is in force, and end
  // every session — which is the one action you want when you think a token
  // has leaked, and which cannot widen anything.
  let authState = $state<AuthState | null>(null);
  let sessionCount = $state<number | null>(null);
  let revoking = $state(false);

  $effect(() => {
    if (section !== "security") return;
    const controller = new AbortController();
    Promise.all([api.authState(controller.signal), api.sessions(controller.signal)])
      .then(([a, s]) => {
        authState = a;
        sessionCount = s.count;
      })
      .catch(() => {});
    return () => controller.abort();
  });

  const AUTH_MODE_LABEL: Record<string, string> = {
    none: "None — anyone who can reach this port has full read access",
    proxy: "Reverse proxy header",
    password: "Password",
    "proxy+password": "Reverse proxy header, with a password fallback",
  };

  // Account management. Read-only fields sit beside these because the rest of
  // the section is the boundary; these three are the parts the account owns
  // and can therefore change about itself.
  let currentPassword = $state("");
  let newPassword = $state("");
  let confirmPassword = $state("");
  let changing = $state(false);
  let togglingAccount = $state(false);

  const minimum = $derived(authState?.min_password_length ?? 10);
  const canChange = $derived(
    currentPassword !== "" &&
      newPassword.length >= minimum &&
      newPassword === confirmPassword,
  );

  let importError = $state<string | null>(null);

  /** Download the override document. A plain navigation: the server sets the
      filename, and there is nothing to build client-side. */
  function exportSettings() {
    window.location.href = "/api/settings/export";
  }

  /** Restore one. The file's `settings` object is exactly what PUT takes, so
      the import is the ordinary write and gets the ordinary validation. */
  async function importSettings(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = "";
    if (!file) return;

    importError = null;
    try {
      const doc = JSON.parse(await file.text());
      const patch = doc?.settings ?? doc;
      if (!patch || typeof patch !== "object" || Array.isArray(patch)) {
        throw new Error("that file has no settings in it");
      }
      adopt(await api.updateSettings(patch));
      notice = doc?.omitted?.length
        ? `Restored. Set again by hand: ${doc.omitted.join(", ")}.`
        : "Restored.";
      error = null;
    } catch (err) {
      importError = (err as Error).message;
    }
  }

  async function refreshAuthState() {
    try {
      authState = await api.authState();
    } catch {
      // Leave the last good answer on screen; the action's own error already
      // said what went wrong.
    }
  }

  const canSetFirst = $derived(
    newPassword.length >= minimum && newPassword === confirmPassword,
  );

  async function setFirstPassword() {
    changing = true;
    try {
      await api.setupAccount(newPassword);
      newPassword = "";
      confirmPassword = "";
      error = null;
      notice = "Password set. You can now sign in without your provider.";
      await refreshAuthState();
    } catch (err) {
      error = (err as Error).message;
      notice = null;
    } finally {
      changing = false;
    }
  }

  async function changePassword() {
    changing = true;
    try {
      await api.changePassword(currentPassword, newPassword);
      currentPassword = "";
      newPassword = "";
      confirmPassword = "";
      error = null;
      notice = "Password changed. Every other signed-in browser was signed out.";
      await refreshAuthState();
    } catch (err) {
      error = (err as Error).message;
      notice = null;
    } finally {
      changing = false;
    }
  }

  async function setAccountEnabled(enabled: boolean) {
    togglingAccount = true;
    try {
      await api.setAccountEnabled(enabled);
      error = null;
      if (!enabled) {
        // Disabling it ended this session too, so there is nothing left to
        // render from here.
        location.reload();
        return;
      }
      notice = "The built-in account is on again.";
      await refreshAuthState();
    } catch (err) {
      error = (err as Error).message;
      notice = null;
    } finally {
      togglingAccount = false;
    }
  }

  async function unlink() {
    try {
      await api.unlinkAccount();
      error = null;
      notice = "Unlinked. That provider identity no longer reaches this account.";
      await refreshAuthState();
    } catch (err) {
      error = (err as Error).message;
    }
  }

  async function revokeAll() {
    revoking = true;
    try {
      await api.revokeSessions();
      // Every session went, including this one, so the app has to re-check.
      location.reload();
    } catch (err) {
      error = (err as Error).message;
      revoking = false;
    }
  }

  function list(value: string): string[] {
    return value.split(",").map((s) => s.trim()).filter(Boolean);
  }

  function multiline(value: string): string[] {
    return value.split(/[\n,]/).map((s) => s.trim()).filter(Boolean);
  }

  // Only what actually differs from what is in force is sent. A patch that
  // restated every field would turn every save into thirteen overrides, and
  // the whole point of the baseline is that most fields never leave it.
  function buildPatch(): SettingsPatch {
    const patch: SettingsPatch = {};
    if (!settings) return patch;
    const e = settings.effective;

    if (draft.snapshot_interval_ms !== e.snapshot_interval_ms)
      patch.snapshot_interval_ms = Number(draft.snapshot_interval_ms);
    if (draft.retention_days !== e.retention_days) patch.retention_days = Number(draft.retention_days);
    if (draft.unchanged_retention_days !== e.unchanged_retention_days)
      patch.unchanged_retention_days = Number(draft.unchanged_retention_days);
    if (draft.event_retention_days !== e.event_retention_days)
      patch.event_retention_days = Number(draft.event_retention_days);
    if (draft.audit_retention_days !== e.audit_retention_days)
      patch.audit_retention_days = Number(draft.audit_retention_days);
    if (draft.retention_interval_ms !== e.retention_interval_ms)
      patch.retention_interval_ms = Number(draft.retention_interval_ms);
    if (draft.vacuum_interval_ms !== e.vacuum_interval_ms)
      patch.vacuum_interval_ms = Number(draft.vacuum_interval_ms);
    if (draft.base_url !== e.base_url) patch.base_url = draft.base_url;
    if (draft.log_level !== e.log_level) patch.log_level = draft.log_level as SettingsPatch["log_level"];
    if (draft.notify_min_severity !== e.notify_min_severity)
      patch.notify_min_severity = draft.notify_min_severity as SettingsPatch["notify_min_severity"];

    const keep = list(draft.keep_keys);
    if (keep.join(",") !== e.keep_keys.join(",")) patch.keep_keys = keep;
    const on = list(draft.notify_on);
    if (on.join(",") !== e.notify_on.join(",")) patch.notify_on = on;

    if (notifyUrls.trim() !== "") patch.notify_urls = multiline(notifyUrls);
    if (ingestToken.trim() !== "") patch.ingest_token = ingestToken.trim();
    return patch;
  }

  const dirty = $derived.by(() => {
    void [draft, settings, notifyUrls, ingestToken];
    return Object.keys(buildPatch()).length > 0;
  });

  async function apply(fn: () => Promise<Settings>, message?: string) {
    saving = true;
    try {
      adopt(await fn());
      error = null;
      notice = message ?? null;
    } catch (err) {
      error = (err as Error).message;
      notice = null;
    } finally {
      saving = false;
    }
  }

  const save = () => {
    const patch = buildPatch();
    if (Object.keys(patch).length === 0) return;
    return apply(() => api.updateSettings(patch), "Saved. In force now — no restart needed.");
  };
  const useEnvironment = (field: string) => apply(() => api.updateSettings({ reset: [field] }));
  const resetAll = () =>
    apply(() => api.resetSettings(), "Every override dropped. Silt is running what its environment says.");

  function revert() {
    if (settings) adopt(settings);
    notice = null;
  }

  async function prune() {
    pruning = true;
    try {
      pruned = await api.prune();
      settings = await api.settings();
      error = null;
    } catch (err) {
      error = (err as Error).message;
    } finally {
      pruning = false;
    }
  }

  const MINUTE = 60_000;
  const HOUR = 3_600_000;
  const DAY = 86_400_000;

  const INTERVALS = [
    ["1 minute", MINUTE],
    ["5 minutes", 5 * MINUTE],
    ["15 minutes", 15 * MINUTE],
    ["30 minutes", 30 * MINUTE],
    ["1 hour", HOUR],
    ["6 hours", 6 * HOUR],
  ] as const;
  const RETENTION_INTERVALS = [
    ["15 minutes", 15 * MINUTE],
    ["1 hour", HOUR],
    ["6 hours", 6 * HOUR],
    ["24 hours", DAY],
  ] as const;
  const VACUUM_INTERVALS = [
    ["disabled", 0],
    ["weekly", 7 * DAY],
    ["monthly", 30 * DAY],
  ] as const;

  const input =
    "w-full rounded-md border border-border bg-background px-2.5 py-1.5 text-sm outline-none focus:ring-2 focus:ring-ring";

  // A live sample, so a date order is chosen by looking at it rather than by
  // decoding "dmy".
  const dateSample = $derived((style: DateStyle) => sampleDate(style, prefs.clock));
</script>

{#snippet field(name: string, label: string, envVar: string, hint: string | undefined, control: Snippet)}
  <div class="grid gap-1.5 py-3.5 sm:grid-cols-[15rem_1fr] sm:gap-6">
    <div class="min-w-0">
      <div class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        <label for={name} class="text-sm font-medium">{label}</label>
        {#if overridden.has(name)}
          <span class="rounded bg-secondary px-1 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
            set here
          </span>
        {/if}
      </div>
      {#if hint}
        <p class="mt-1 text-xs leading-relaxed text-muted-foreground/70">{hint}</p>
      {/if}
      <p class="mt-1 font-mono text-[10px] text-muted-foreground/40">{envVar}</p>
      {#if overridden.has(name)}
        <button
          type="button"
          class="mt-1 text-[11px] text-muted-foreground underline underline-offset-2 hover:text-foreground"
          onclick={() => useEnvironment(name)}
          disabled={saving || readOnly}
        >
          use the environment value
        </button>
      {/if}
    </div>
    <div class="min-w-0 max-w-md">{@render control()}</div>
  </div>
{/snippet}

{#snippet choice(name: string, label: string, hint: string | undefined, control: Snippet)}
  <div class="grid gap-1.5 py-3.5 sm:grid-cols-[15rem_1fr] sm:gap-6">
    <div class="min-w-0">
      <label for={name} class="text-sm font-medium">{label}</label>
      {#if hint}
        <p class="mt-1 text-xs leading-relaxed text-muted-foreground/70">{hint}</p>
      {/if}
    </div>
    <div class="min-w-0 max-w-md">{@render control()}</div>
  </div>
{/snippet}

<div class="flex flex-col gap-6 lg:flex-row">
  <!-- The section rail. Sticky rather than scrolling with the pane, so you can
       always get from Retention to Storage without scrolling back up. -->
  <nav class="shrink-0 lg:sticky lg:top-0 lg:w-56 lg:self-start" aria-label="Settings sections">
    <h2 class="mb-3 hidden text-lg font-semibold tracking-tight lg:block">Settings</h2>

    <!-- Search before the list, because with nine sections it is the faster
         route to most settings — and the only route for anyone who knows the
         setting by its environment variable rather than by which screen it
         was filed under. -->
    <div class="relative mb-3">
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
           class="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground" aria-hidden="true">
        <circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" />
      </svg>
      <input
        bind:this={searchBox}
        bind:value={query}
        type="search"
        placeholder="Search settings…"
        aria-label="Search settings"
        class="w-full rounded-md border border-border bg-background py-1.5 pl-8 pr-7 text-xs outline-none
               focus:ring-2 focus:ring-ring"
        onkeydown={(e) => {
          if (e.key === "Escape") query = "";
          if (e.key === "Enter" && hits.length > 0) goTo(hits[0].section);
        }}
      />
      {#if query}
        <button
          type="button"
          class="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
          aria-label="Clear search"
          onclick={() => { query = ""; searchBox?.focus(); }}
        >
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
            <path d="M18 6 6 18M6 6l12 12" />
          </svg>
        </button>
      {/if}
    </div>

    {#if query}
      <!-- Results replace the rail rather than sitting beside it: while you
           are searching, the section list is not what you are looking at. -->
      <div class="space-y-0.5">
        {#if hits.length === 0}
          <p class="px-2.5 py-1.5 text-xs text-muted-foreground">
            Nothing matches “{query}”.
          </p>
        {:else}
          {#each hits as hit (hit.name)}
            <button
              type="button"
              class="w-full rounded-md px-2.5 py-1.5 text-left transition-colors hover:bg-secondary/50"
              onclick={() => goTo(hit.section)}
            >
              <span class="block text-sm">{hit.label}</span>
              <span class="block text-[11px] text-muted-foreground">
                {hit.sectionLabel}{#if hit.env}{" · "}<span class="font-mono">{hit.env}</span>{/if}
              </span>
            </button>
          {/each}
        {/if}
      </div>
    {:else}
      <div class="flex gap-1 overflow-x-auto pb-1 lg:flex-col lg:overflow-visible lg:pb-0">
        {#each SECTIONS as s (s.id)}
          <button
            type="button"
            class="flex shrink-0 items-center justify-between gap-2 whitespace-nowrap rounded-md px-2.5 py-1.5 text-left text-sm transition-colors
                   {section === s.id
              ? 'bg-secondary text-secondary-foreground'
              : 'text-muted-foreground hover:bg-secondary/50 hover:text-foreground'}"
            onclick={() => (section = s.id)}
          >
            {s.label}
            <!-- How many of this section's settings are set here rather than
                 by the environment. Without it, finding your own overrides
                 means opening all nine. -->
            {#if s.id === "setup" && attention > 0}
              <span class="rounded bg-amber-500/15 px-1 text-[10px] tabular-nums text-amber-600 dark:text-amber-400">
                {attention}
              </span>
            {:else if counts[s.id]}
              <span class="rounded bg-background/60 px-1 text-[10px] tabular-nums text-muted-foreground">
                {counts[s.id]}
              </span>
            {/if}
          </button>
        {/each}
      </div>
    {/if}
    {#if settings && overridden.size > 0}
      <button
        type="button"
        class="mt-4 hidden text-[11px] text-muted-foreground underline underline-offset-2 hover:text-foreground lg:block"
        onclick={resetAll}
        disabled={saving || readOnly}
      >
        Use the environment for everything
      </button>
    {/if}
  </nav>

  <div class="min-w-0 flex-1 space-y-4">
    {#if error}
      <p class="rounded-md border border-red-500/40 bg-red-500/10 px-4 py-2.5 text-sm text-red-500 dark:text-red-300">
        {error}
      </p>
    {/if}
    {#if notice}
      <p class="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-4 py-2.5 text-sm text-emerald-600 dark:text-emerald-300">
        {notice}
      </p>
    {/if}

    {#if readOnly}
      <p class="rounded-md border border-border bg-secondary/40 px-4 py-2.5 text-sm text-muted-foreground">
        You have read-only access. Every screen is yours to read; changing Silt's own
        configuration needs an administrator. Appearance still works — those settings live in this
        browser, not in Silt.
      </p>
    {/if}

    <!-- The draft survives switching section, but the save bar does not: it
         only renders where there is something to save. So an edit made under
         Retention and then abandoned for Storage was still pending with
         nothing on screen saying so. -->
    {#if dirty && READ_ONLY_SECTIONS.has(section)}
      <div class="flex flex-wrap items-center gap-3 rounded-md border border-amber-500/40 bg-amber-500/10 px-4 py-2.5 text-sm">
        <span>You have unsaved changes in another section.</span>
        <Button size="sm" onclick={save} disabled={saving}>
          {saving ? "Saving…" : "Save them"}
        </Button>
        <Button variant="ghost" size="sm" onclick={revert} disabled={saving}>Discard</Button>
      </div>
    {/if}

    {#if section === "appearance"}
      <section>
        <h3 class="text-sm font-semibold">Appearance</h3>
        <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
          These live in this browser, not in Silt. A 24-hour clock or a dd/mm/yyyy date is a
          property of whoever is reading the screen, not of the install, so two people looking at
          the same Silt each get their own.
        </p>

        <div class="mt-2 divide-y divide-border">
          {#snippet layoutControl()}
            <div class="flex rounded-md border border-border">
              {#each [["top", "Top bar"], ["side", "Left rail"]] as [value, label] (value)}
                <button
                  type="button"
                  class="flex-1 px-3 py-1.5 text-xs transition-colors first:rounded-l-md last:rounded-r-md
                         {prefs.layout === value
                    ? 'bg-secondary text-secondary-foreground'
                    : 'text-muted-foreground hover:text-foreground'}"
                  onclick={() => prefs.set("layout", value as Layout)}
                >
                  {label}
                </button>
              {/each}
            </div>
          {/snippet}
          {#snippet themeControl()}
            <Segmented
              label="Theme"
              options={[
                { value: "light", label: "Light" },
                { value: "dark", label: "Dark" },
                { value: "system", label: "System" },
              ]}
              value={theme.value}
              onchange={(next) => theme.set(next as Theme)}
            />
          {/snippet}
          {@render choice(
            "theme",
            "Theme",
            "Silt is dark by default. \u201CSystem\u201D follows whatever this device is set to, which a two-way toggle cannot express \u2014 which is why the old one silently pinned you to one or the other.",
            themeControl,
          )}

          {@render choice(
            "layout",
            "Navigation",
            "Sections across the top, or stacked in the left rail above the project list.",
            layoutControl,
          )}

          {#snippet clockControl()}
            <select id="clock" class={input} value={prefs.clock} onchange={(e) => prefs.set("clock", e.currentTarget.value as Clock)}>
              <option value="system">Follow this device</option>
              <option value="h24">24-hour (14:30)</option>
              <option value="h12">12-hour (2:30 PM)</option>
            </select>
          {/snippet}
          {@render choice("clock", "Clock", undefined, clockControl)}

          {#snippet dateControl()}
            <select
              id="dateStyle"
              class={input}
              value={prefs.dateStyle}
              onchange={(e) => prefs.set("dateStyle", e.currentTarget.value as DateStyle)}
            >
              <option value="system">Follow this device — {dateSample("system")}</option>
              <option value="dmy">Day first — {dateSample("dmy")}</option>
              <option value="mdy">Month first — {dateSample("mdy")}</option>
              <option value="ymd">Year first — {dateSample("ymd")}</option>
            </select>
          {/snippet}
          {@render choice("dateStyle", "Date order", undefined, dateControl)}

          {#snippet stampControl()}
            <select
              id="timestamps"
              class={input}
              value={prefs.timestamps}
              onchange={(e) => prefs.set("timestamps", e.currentTarget.value as TimeStamps)}
            >
              <option value="relative">Relative — 3m ago</option>
              <option value="absolute">Absolute — {dateSample(prefs.dateStyle)}</option>
            </select>
            <label class="mt-2 flex items-center gap-2 text-xs text-muted-foreground">
              <input
                type="checkbox"
                checked={prefs.seconds}
                onchange={(e) => prefs.set("seconds", e.currentTarget.checked)}
                class="accent-emerald-500"
              />
              Show seconds
            </label>
          {/snippet}
          {@render choice(
            "timestamps",
            "Timestamps",
            "Relative reads faster on a live page; absolute is what you want when you are lining Silt up against another tool's logs. The other form is always in the tooltip.",
            stampControl,
          )}
        </div>
      </section>
    {/if}

    {#if settings}
      {#if section === "collection"}
        <section>
          <h3 class="text-sm font-semibold">Collection</h3>
          <div class="mt-2 divide-y divide-border">
            {#snippet intervalControl()}
              <select id="snapshot_interval_ms" bind:value={draft.snapshot_interval_ms} class={input}>
                {#each INTERVALS as [label, ms] (ms)}
                  <option value={ms}>{label}</option>
                {/each}
                {#if !INTERVALS.some(([, ms]) => ms === draft.snapshot_interval_ms)}
                  <option value={draft.snapshot_interval_ms}>{duration(draft.snapshot_interval_ms)}</option>
                {/if}
              </select>
            {/snippet}
            {@render field(
              "snapshot_interval_ms",
              "Reconcile interval",
              "SILT_SNAPSHOT_INTERVAL",
              "Silt records changes as Docker reports them; this is the sweep that catches whatever the event stream missed.",
              intervalControl,
            )}

            {#snippet keepControl()}
              <input id="keep_keys" bind:value={draft.keep_keys} placeholder="PUID, TZ, MY_APP_*" class={input} />
            {/snippet}
            {@render field(
              "keep_keys",
              "Keys kept readable",
              "SILT_KEEP_KEYS",
              "Every environment value is a keyed digest unless its key is on the safe list. These are the extras you added, comma separated; one leading or trailing * is allowed.",
              keepControl,
            )}

            {#snippet logControl()}
              <select id="log_level" bind:value={draft.log_level} class={input}>
                {#each ["debug", "info", "warn", "error"] as level (level)}
                  <option value={level}>{level}</option>
                {/each}
              </select>
            {/snippet}
            {@render field("log_level", "Log level", "SILT_LOG_LEVEL", undefined, logControl)}
          </div>
        </section>
      {/if}

      {#if section === "retention"}
        <section>
          <h3 class="text-sm font-semibold">Retention</h3>
          <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
            Zero means keep forever. Runtime-only snapshots are the proof-of-liveness rows between
            changes, and cannot outlive the changes they sit between.
          </p>
          <div class="mt-2 divide-y divide-border">
            {#snippet changedControl()}
              <div class="flex items-center gap-2">
                <input id="retention_days" type="number" min="0" bind:value={draft.retention_days} class={input} />
                <span class="shrink-0 text-xs text-muted-foreground">days</span>
              </div>
            {/snippet}
            {@render field("retention_days", "Changed snapshots", "SILT_RETENTION_DAYS", undefined, changedControl)}

            {#snippet unchangedControl()}
              <div class="flex items-center gap-2">
                <input
                  id="unchanged_retention_days"
                  type="number"
                  min="0"
                  bind:value={draft.unchanged_retention_days}
                  class={input}
                />
                <span class="shrink-0 text-xs text-muted-foreground">days</span>
              </div>
            {/snippet}
            {@render field(
              "unchanged_retention_days",
              "Runtime-only snapshots",
              "SILT_UNCHANGED_RETENTION_DAYS",
              undefined,
              unchangedControl,
            )}

            {#snippet eventControl()}
              <div class="flex items-center gap-2">
                <input
                  id="event_retention_days"
                  type="number"
                  min="0"
                  bind:value={draft.event_retention_days}
                  class={input}
                />
                <span class="shrink-0 text-xs text-muted-foreground">days</span>
              </div>
            {/snippet}
            {@render field("event_retention_days", "Events", "SILT_EVENT_RETENTION_DAYS", undefined, eventControl)}

            {#snippet auditControl()}
              <div class="flex items-center gap-2">
                <input
                  id="audit_retention_days"
                  type="number"
                  min="0"
                  bind:value={draft.audit_retention_days}
                  class={input}
                />
                <span class="shrink-0 text-xs text-muted-foreground">days</span>
              </div>
            {/snippet}
            {@render field(
              "audit_retention_days",
              "Activity trail",
              "SILT_AUDIT_RETENTION_DAYS",
              "Who changed Silt itself — the list under Security. A row per administrative action rather than per observation, so it stays tiny, and its whole value is how far back it reaches. 0 keeps it forever.",
              auditControl,
            )}

            {#snippet passControl()}
              <select id="retention_interval_ms" bind:value={draft.retention_interval_ms} class={input}>
                {#each RETENTION_INTERVALS as [label, ms] (ms)}
                  <option value={ms}>{label}</option>
                {/each}
                {#if !RETENTION_INTERVALS.some(([, ms]) => ms === draft.retention_interval_ms)}
                  <option value={draft.retention_interval_ms}>{duration(draft.retention_interval_ms)}</option>
                {/if}
              </select>
            {/snippet}
            {@render field(
              "retention_interval_ms",
              "Retention pass runs every",
              "SILT_RETENTION_INTERVAL",
              undefined,
              passControl,
            )}

            {#snippet vacuumControl()}
              <select id="vacuum_interval_ms" bind:value={draft.vacuum_interval_ms} class={input}>
                {#each VACUUM_INTERVALS as [label, ms] (ms)}
                  <option value={ms}>{label}</option>
                {/each}
                {#if !VACUUM_INTERVALS.some(([, ms]) => ms === draft.vacuum_interval_ms)}
                  <option value={draft.vacuum_interval_ms}>{duration(draft.vacuum_interval_ms)}</option>
                {/if}
              </select>
            {/snippet}
            {@render field(
              "vacuum_interval_ms",
              "Vacuum",
              "SILT_VACUUM_INTERVAL",
              "Reclaims free pages by rewriting the whole file. Cheap to skip, expensive to run.",
              vacuumControl,
            )}
          </div>
        </section>
      {/if}

      {#if section === "notifications"}
        <section>
          <h3 class="text-sm font-semibold">Notifications</h3>
          <div class="mt-2 divide-y divide-border">
            {#snippet targetsControl()}
              {#if (settings?.effective.notify_targets.length ?? 0) > 0}
                <ul class="mb-2 space-y-1">
                  {#each settings?.effective.notify_targets ?? [] as target, i (i)}
                    <li class="font-mono text-xs text-muted-foreground">{target}</li>
                  {/each}
                </ul>
              {:else}
                <p class="mb-2 font-mono text-xs text-muted-foreground">none configured</p>
              {/if}
              <textarea
                id="notify_urls"
                bind:value={notifyUrls}
                rows="3"
                placeholder="gotify://gotify.example.com/AppToken&#10;discord://token@id"
                class="{input} font-mono text-xs"
              ></textarea>

              {#if (settings?.effective.notify_targets.length ?? 0) > 0}
                <div class="mt-2 flex flex-wrap items-center gap-2">
                  <button
                    type="button"
                    onclick={sendTestNotification}
                    disabled={notifyTesting}
                    class="rounded-md border border-border px-2.5 py-1.5 text-xs transition-colors hover:bg-secondary/60 disabled:opacity-50"
                  >
                    {notifyTesting ? "Sending…" : "Send a test"}
                  </button>
                  <span class="text-[11px] text-muted-foreground">
                    Tests what is saved, not what is typed above.
                  </span>
                </div>
              {/if}

              {#if notifyTestError}
                <p class="mt-2 text-xs text-red-600 dark:text-red-400">{notifyTestError}</p>
              {/if}
              {#if notifyTest}
                <ul class="mt-2 space-y-1">
                  {#each notifyTest.results as result (result.index)}
                    <!-- The reason sits under its target rather than beside
                         it: a provider error is long enough to wrap, and a
                         wrapped one reads as belonging to nothing. -->
                    <li class="text-xs">
                      <div class="flex items-baseline gap-2">
                        <span
                          class={result.ok
                            ? "text-emerald-600 dark:text-emerald-400"
                            : "text-red-600 dark:text-red-400"}
                        >
                          {result.ok ? "sent" : "failed"}
                        </span>
                        <span class="min-w-0 truncate font-mono text-[11px] text-muted-foreground">
                          {result.target}
                        </span>
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
            {/snippet}
            {@render field(
              "notify_urls",
              "Targets",
              "SILT_NOTIFY_URLS",
              "shoutrrr URLs, one per line. A shoutrrr URL carries the credential for the service it points at, so Silt shows what is configured but never hands the URL back. Typing here replaces the whole list.",
              targetsControl,
            )}

            {#snippet kindsControl()}
              <input id="notify_on" bind:value={draft.notify_on} class="{input} font-mono text-xs" />
            {/snippet}
            {@render field(
              "notify_on",
              "Notify on",
              "SILT_NOTIFY_ON",
              "Change kinds, comma separated, or `all`.",
              kindsControl,
            )}

            {#snippet severityControl()}
              <select id="notify_min_severity" bind:value={draft.notify_min_severity} class={input}>
                {#each ["low", "medium", "high"] as level (level)}
                  <option value={level}>{level}</option>
                {/each}
              </select>
            {/snippet}
            {@render field(
              "notify_min_severity",
              "Minimum severity",
              "SILT_NOTIFY_MIN_SEVERITY",
              "ANDed with the kinds above.",
              severityControl,
            )}

            {#snippet baseControl()}
              <input id="base_url" bind:value={draft.base_url} placeholder="https://silt.example.lan" class={input} />
            {/snippet}
            {@render field(
              "base_url",
              "Base URL",
              "SILT_BASE_URL",
              "Where Silt is reachable, used to build the link in a notification. Empty omits the link.",
              baseControl,
            )}
          </div>
        </section>
      {/if}

      {#if section === "ingest"}
        <section>
          <h3 class="text-sm font-semibold">Ingest webhook</h3>
          <div class="mt-2 divide-y divide-border">
            {#snippet tokenControl()}
              <input
                id="ingest_token"
                type="password"
                autocomplete="new-password"
                bind:value={ingestToken}
                placeholder={settings?.effective.ingest_configured ? "•••••••• — type to replace" : "not configured"}
                class="{input} font-mono text-xs"
              />
              {#if settings?.effective.ingest_configured}
                <button
                  type="button"
                  class="mt-2 text-[11px] text-muted-foreground underline underline-offset-2 hover:text-foreground"
                  onclick={() => apply(() => api.updateSettings({ ingest_token: "" }))}
                  disabled={saving}
                >
                  Turn the ingest endpoint off
                </button>
              {/if}
            {/snippet}
            {@render field(
              "ingest_token",
              "Token",
              "SILT_INGEST_TOKEN",
              settings.effective.ingest_configured
                ? "Guards POST /api/ingest, and is configured. Typing here replaces it."
                : "Guards POST /api/ingest. Not configured, so the endpoint refuses every request.",
              tokenControl,
            )}
          </div>
        </section>
      {/if}

      {#if section === "security"}
        <section>
          <h3 class="text-sm font-semibold">Security</h3>
          <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
            Nothing here is editable, and that is the point: every one of these is the boundary
            protecting this screen. A UI that could turn off the login in front of it would be a way
            in rather than a setting. Change them in your environment and recreate the container.
          </p>

          <dl class="mt-3 divide-y divide-border">
            {#snippet row(label: string, value: string, envVar: string, hint?: string)}
              <div class="grid gap-1 py-2.5 text-sm sm:grid-cols-[15rem_1fr] sm:gap-6">
                <dt class="min-w-0">
                  {label}
                  <span class="mt-0.5 block font-mono text-[10px] text-muted-foreground/40">{envVar}</span>
                </dt>
                <dd class="min-w-0">
                  <span class="break-all font-mono text-xs">{value}</span>
                  {#if hint}
                    <p class="mt-1 text-xs leading-relaxed text-muted-foreground/70">{hint}</p>
                  {/if}
                </dd>
              </div>
            {/snippet}

            {@render row(
              "Sign-in method",
              AUTH_MODE_LABEL[settings.fixed.auth_mode] ?? settings.fixed.auth_mode,
              "SILT_OIDC_ISSUER / SILT_TRUST_PROXY_AUTH / SILT_PASSWORD_HASH",
              settings.fixed.auth_mode === "none"
                ? "Silt is open. That is the right default for something behind your own proxy, and the wrong one for anything else."
                : undefined,
            )}

            {#if authState?.oidc_enabled}
              {@render row("Identity provider", authState.oidc_issuer ?? "configured", "SILT_OIDC_ISSUER")}
            {:else if authState?.oidc_error}
              {@render row(
                "Identity provider",
                "configured, but unreachable",
                "SILT_OIDC_ISSUER",
                authState.oidc_error,
              )}
            {/if}

            {@render row(
              "Signed-in sessions",
              sessionCount === null ? "…" : String(sessionCount),
              "SILT_SESSION_TTL / SILT_SESSION_IDLE_TTL",
              "Sessions are rows in Silt's database, not signed cookies. Signing out revokes one; the button below revokes all of them.",
            )}
          </dl>

          {#if authState?.local_available}
            <h4 class="mt-8 text-sm font-medium">Built-in account</h4>
            {#if authState.local_managed}
              <p class="mt-1 max-w-xl text-xs leading-relaxed text-muted-foreground/70">
                The password comes from <span class="font-mono">SILT_PASSWORD_HASH</span>, so it is
                not this screen's to change. Unset that variable if you would rather manage it here.
              </p>
            {:else if authState.setup_required}
              <p class="mt-1 max-w-xl text-xs leading-relaxed text-muted-foreground/70">
                This account has no password yet. Set one and you can sign in without your
                provider — useful for the day the provider is the thing that is down.
              </p>
              <div class="mt-3 max-w-md space-y-3">
                <div>
                  <label class="block text-xs text-muted-foreground" for="new-password">
                    Password <span class="text-muted-foreground/60">· at least {minimum} characters</span>
                  </label>
                  <input
                    id="new-password"
                    type="password"
                    autocomplete="new-password"
                    bind:value={newPassword}
                    class="{input} mt-1"
                  />
                </div>
                <div>
                  <label class="block text-xs text-muted-foreground" for="confirm-password">Confirm</label>
                  <input
                    id="confirm-password"
                    type="password"
                    autocomplete="new-password"
                    bind:value={confirmPassword}
                    class="{input} mt-1"
                  />
                  {#if confirmPassword !== "" && confirmPassword !== newPassword}
                    <p class="mt-1 text-xs text-amber-600 dark:text-amber-400">These do not match.</p>
                  {/if}
                </div>
                <Button size="sm" onclick={setFirstPassword} disabled={!canSetFirst || changing}>
                  {changing ? "Setting…" : "Set password"}
                </Button>
              </div>
            {:else if authState.local_enabled}
              <div class="mt-3 max-w-md space-y-3">
                <div>
                  <label class="block text-xs text-muted-foreground" for="current-password">
                    Current password
                  </label>
                  <input
                    id="current-password"
                    type="password"
                    autocomplete="current-password"
                    bind:value={currentPassword}
                    class="{input} mt-1"
                  />
                </div>
                <div>
                  <label class="block text-xs text-muted-foreground" for="new-password">
                    New password <span class="text-muted-foreground/60">· at least {minimum} characters</span>
                  </label>
                  <input
                    id="new-password"
                    type="password"
                    autocomplete="new-password"
                    bind:value={newPassword}
                    class="{input} mt-1"
                  />
                </div>
                <div>
                  <label class="block text-xs text-muted-foreground" for="confirm-password">Confirm</label>
                  <input
                    id="confirm-password"
                    type="password"
                    autocomplete="new-password"
                    bind:value={confirmPassword}
                    class="{input} mt-1"
                  />
                  {#if confirmPassword !== "" && confirmPassword !== newPassword}
                    <p class="mt-1 text-xs text-amber-600 dark:text-amber-400">These do not match.</p>
                  {/if}
                </div>
                <Button size="sm" onclick={changePassword} disabled={!canChange || changing}>
                  {changing ? "Changing…" : "Change password"}
                </Button>
                <p class="text-xs leading-relaxed text-muted-foreground/70">
                  Changing it signs every other browser out, so doing this because you think it
                  leaked also ends whatever leaked.
                </p>
              </div>
            {:else}
              <p class="mt-1 max-w-xl text-xs leading-relaxed text-muted-foreground/70">
                Password sign-in is turned off for this account. It still exists, and a linked
                provider identity still reaches it.
              </p>
            {/if}

            {#if authState.oidc_enabled}
              <div class="mt-5">
                {#if authState.local_linked}
                  <p class="text-xs leading-relaxed text-muted-foreground">
                    Linked to a provider identity — signing in with it reaches this account,
                    whatever the group allowlists say.
                  </p>
                  <Button variant="outline" size="sm" class="mt-2" onclick={unlink}>Unlink</Button>
                {:else}
                  <p class="max-w-xl text-xs leading-relaxed text-muted-foreground">
                    Link this account to your provider identity, and signing in there reaches the
                    same account. That is what lets you turn the password off and keep the account.
                  </p>
                  <Button variant="outline" size="sm" class="mt-2" onclick={() => api.linkAccount()}>
                    Link to my provider identity
                  </Button>
                {/if}
              </div>
            {/if}

            <div class="mt-5">
              {#if authState.local_enabled}
                <Button
                  variant="outline"
                  size="sm"
                  onclick={() => setAccountEnabled(false)}
                  disabled={togglingAccount || (!authState.oidc_enabled && !authState.proxy_enabled)}
                >
                  Turn the built-in account off
                </Button>
                {#if !authState.oidc_enabled && !authState.proxy_enabled}
                  <p class="mt-1.5 max-w-xl text-xs text-muted-foreground/70">
                    Not while it is the only way in. Configure a provider or a reverse proxy first.
                  </p>
                {/if}
              {:else}
                <Button variant="outline" size="sm" onclick={() => setAccountEnabled(true)} disabled={togglingAccount}>
                  Turn the built-in account on
                </Button>
              {/if}
            </div>
          {/if}

          <h4 class="mt-8 text-sm font-medium">Sessions</h4>
          <div class="mt-3">
            <Button variant="outline" size="sm" onclick={revokeAll} disabled={revoking || !authState?.required}>
              {revoking ? "Signing out…" : "Sign out everywhere"}
            </Button>
            <p class="mt-1.5 max-w-xl text-xs text-muted-foreground/70">
              {#if authState?.required}
                Ends every session, including this one. Use it if you think a session token has
                leaked — a cookie the browser throws away is still a working credential to anyone
                who copied it, but a deleted row is not.
              {:else}
                There is nothing to revoke while no authentication is configured.
              {/if}
            </p>
          </div>

          <div class="mt-8">
            <AuditLog />
          </div>
        </section>
      {/if}

      {#if section === "setup"}
        <SetupChecks checks={settings.checks} />
      {/if}

      {#if section === "identity"}
        {@const id = settings.identity}
        <section>
          <h3 class="text-sm font-semibold">Authentication</h3>
          <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
            How this install decides who you are. Read-only, and for the sharper of the two reasons
            the environment-only settings are: these are the boundary protecting this screen, so a
            UI that could edit them would be a way in rather than a setting. Shown at all because
            twelve variables were readable nowhere — and when forward auth is not working, the first
            question is what Silt thinks it was told.
          </p>

          {#snippet detail(label: string, value: string, envVar: string, hint?: string)}
            <div class="grid gap-1 py-2.5 text-sm sm:grid-cols-[15rem_1fr] sm:gap-6">
              <dt class="min-w-0">
                {label}
                {#if hint}
                  <span class="mt-0.5 block text-xs leading-relaxed text-muted-foreground/70">{hint}</span>
                {/if}
                {#if envVar}
                  <span class="mt-0.5 block font-mono text-[10px] text-muted-foreground/40">{envVar}</span>
                {/if}
              </dt>
              <dd class="min-w-0 break-all font-mono text-xs {value === 'not set' ? 'text-muted-foreground/50' : ''}">
                {value}
              </dd>
            </div>
          {/snippet}

          {#snippet flag(label: string, on: boolean, envVar: string, hint?: string)}
            <div class="grid gap-1 py-2.5 text-sm sm:grid-cols-[15rem_1fr] sm:gap-6">
              <dt class="min-w-0">
                {label}
                {#if hint}
                  <span class="mt-0.5 block text-xs leading-relaxed text-muted-foreground/70">{hint}</span>
                {/if}
                <span class="mt-0.5 block font-mono text-[10px] text-muted-foreground/40">{envVar}</span>
              </dt>
              <dd class="min-w-0"><Toggle checked={on} readonly label={label} /></dd>
            </div>
          {/snippet}

          <h4 class="mt-5 text-xs font-medium uppercase tracking-wide text-muted-foreground">In effect</h4>
          <dl class="divide-y divide-border">
            {@render detail(
              "Roles",
              id.roles_enabled
                ? "on — administrators change Silt's configuration, everyone else reads"
                : "off — everyone admitted may change everything",
              "SILT_OIDC_ADMIN_GROUPS / SILT_ADMIN_GROUPS",
            )}
            {@render detail(
              "Method",
              id.mode === "none" ? "none — anyone who can reach this address can read it" : id.mode,
              "",
              "The first of these that is configured wins: an identity provider, then your reverse proxy, then the built-in account.",
            )}
            {@render flag("Built-in account", id.local_account, "SILT_LOCAL_ACCOUNT", "Silt's own administrator. Off leaves only the provider.")}
            {@render flag("Password claimed at startup", id.password_hash_set, "SILT_PASSWORD_HASH", "Set, the account is claimed before Silt starts and the first-run window never exists.")}
            {@render detail("Session lifetime", duration(id.session_ttl_ms), "SILT_SESSION_TTL")}
            {@render detail("Idle timeout", duration(id.session_idle_ttl_ms), "SILT_SESSION_IDLE_TTL")}
            {@render flag("Metrics without signing in", id.metrics_public, "SILT_METRICS_PUBLIC", "/metrics carries counts and names, not values — but a project name is still information about your host.")}
          </dl>

          <h4 class="mt-6 text-xs font-medium uppercase tracking-wide text-muted-foreground">Reverse proxy</h4>
          <dl class="divide-y divide-border">
            {@render flag("Trust an asserted identity", id.trust_proxy_auth, "SILT_TRUST_PROXY_AUTH")}
            {@render detail("Identity header", id.auth_header || "not set", "SILT_AUTH_HEADER")}
            {@render detail(
              "Groups header",
              id.auth_groups_header || "not set",
              "SILT_AUTH_GROUPS_HEADER",
              "Read only when administrator groups are configured: without a rule there is nothing to compare against, and reading an attacker-settable header for no reason is a habit worth not having.",
            )}
            {@render detail(
              "Administrator groups",
              id.admin_groups.join(", ") || "everyone admitted is an administrator",
              "SILT_ADMIN_GROUPS",
            )}
            {@render detail(
              "Trusted proxies",
              id.trusted_proxies.length ? id.trusted_proxies.join(", ") : "not set",
              "SILT_TRUSTED_PROXIES",
              "The whole security of forward auth. The header is settable by anything that can open a socket, so with no list here \u201Cauthenticated\u201D means \u201Creached the port\u201D.",
            )}
          </dl>

          <h4 class="mt-6 text-xs font-medium uppercase tracking-wide text-muted-foreground">OpenID Connect</h4>
          <dl class="divide-y divide-border">
            {@render detail("Issuer", id.oidc_issuer || "not set", "SILT_OIDC_ISSUER")}
            {@render detail("Client ID", id.oidc_client_id || "not set", "SILT_OIDC_CLIENT_ID")}
            {@render flag("Client secret", id.oidc_secret_set, "SILT_OIDC_CLIENT_SECRET", "Set or not, never shown \u2014 like the notification targets and the ingest token.")}
            {@render detail("Redirect URL", id.oidc_redirect_url || "defaults to the base URL + /api/auth/callback", "SILT_OIDC_REDIRECT_URL")}
            {@render detail("Scopes", id.oidc_scopes.join(", ") || "not set", "SILT_OIDC_SCOPES")}
            {@render detail("Username claim", id.oidc_username_claim || "not set", "SILT_OIDC_USERNAME_CLAIM", "Providers disagree about these two, which is the usual reason a sign-in works but names nobody.")}
            {@render detail("Groups claim", id.oidc_groups_claim || "not set", "SILT_OIDC_GROUPS_CLAIM")}
            {@render detail(
              "Allowed groups",
              id.oidc_allowed_groups.join(", ") || "any",
              "SILT_OIDC_ALLOWED_GROUPS",
              "Both allowlists empty admits anyone the provider will authenticate.",
            )}
            {@render detail("Allowed users", id.oidc_allowed_users.join(", ") || "any", "SILT_OIDC_ALLOWED_USERS")}
            {@render detail(
              "Administrator groups",
              id.oidc_admin_groups.join(", ") || "everyone admitted is an administrator",
              "SILT_OIDC_ADMIN_GROUPS",
              "Membership makes an identity an administrator. Empty is what Silt did before roles existed — turning an upgrade into a lockout for the person who configured it would be the worst possible default.",
            )}
          </dl>
        </section>
      {/if}

      {#if section === "environment"}
        <section>
          <h3 class="text-sm font-semibold">Environment only</h3>
          <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
            These cannot be changed here. Some need a restart to take effect; the rest are the
            boundary protecting this screen — a UI that could widen which files Silt reads, or turn
            off the login in front of it, would be a way in rather than a setting.
          </p>
          <dl class="mt-3 divide-y divide-border">
            {#each [["Host name", settings.fixed.host_name, "SILT_HOST_NAME"], ["Docker endpoint", settings.fixed.docker_host, "SILT_DOCKER_HOST"], ["Database", settings.fixed.db_path, "SILT_DB_PATH"], ["Listen address", settings.fixed.listen_addr, "SILT_LISTEN_ADDR"], ["Authentication", settings.fixed.auth_mode, "SILT_TRUST_PROXY_AUTH / SILT_PASSWORD_HASH"], ["Compose roots", settings.fixed.compose_roots.join(", ") || "none — file capture is off", "SILT_COMPOSE_ROOTS"], ["Max compose file", bytes(settings.fixed.max_compose_file_bytes), "SILT_MAX_COMPOSE_FILE_BYTES"]] as [label, value, envVar] (envVar)}
              <div class="grid gap-1 py-2.5 text-sm sm:grid-cols-[15rem_1fr] sm:gap-6">
                <dt class="min-w-0">
                  {label}
                  <span class="mt-0.5 block font-mono text-[10px] text-muted-foreground/40">{envVar}</span>
                </dt>
                <dd class="min-w-0 break-all font-mono text-xs">{value}</dd>
              </div>
            {/each}
          </dl>
        </section>
      {/if}

      {#if section === "storage"}
        <section>
          <h3 class="text-sm font-semibold">Storage</h3>
          <dl class="mt-3 grid grid-cols-2 gap-px overflow-hidden rounded-md border border-border bg-border sm:grid-cols-4">
            {#each [["Blobs", String(settings.usage.blobs)], ["Stored", bytes(settings.usage.stored_bytes)], ["Uncompressed", bytes(settings.usage.uncompressed_bytes)], ["Events", String(settings.usage.events)]] as [label, value] (label)}
              <div class="bg-background p-3">
                <dt class="text-[11px] text-muted-foreground">{label}</dt>
                <dd class="mt-0.5 font-mono text-sm">{value}</dd>
              </div>
            {/each}
          </dl>

          <!-- Settings are already a sparse patch on top of the environment, so
               the export is that document with a header. There is no import
               endpoint: PUT /api/settings already takes this shape, and a
               second write path would be a second set of validation rules to
               keep in step. -->
          <div class="mt-6 border-t border-border pt-5">
            <h4 class="text-sm font-medium">Move this configuration</h4>
            <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
              A file of everything set here rather than by the environment. Secrets are left
              out and named in the file — a notification target restored as a blank is a
              restore that quietly stops notifying, so it says which ones you will have to
              set again.
            </p>
            <div class="mt-3 flex flex-wrap items-center gap-3">
              <Button variant="outline" size="sm" onclick={exportSettings}>Download settings</Button>
              {#if !readOnly}
                <label class="cursor-pointer text-xs text-muted-foreground underline underline-offset-2 hover:text-foreground">
                  <input type="file" accept="application/json,.json" class="sr-only" onchange={importSettings} />
                  Restore from a file
                </label>
              {/if}
              {#if importError}
                <span class="text-xs text-red-500 dark:text-red-300">{importError}</span>
              {/if}
            </div>
          </div>

          <div class="mt-6 border-t border-border pt-5">
            <Button variant="outline" size="sm" onclick={prune} disabled={pruning || readOnly}>
              {pruning ? "Pruning…" : "Run retention pass now"}
            </Button>
          </div>

          {#if pruned}
            <p class="mt-2 text-xs text-muted-foreground">
              Removed {pruned.unchanged_snapshots} runtime-only and {pruned.changed_snapshots} changed
              snapshots, {pruned.events} events, {pruned.blobs} blobs.
            </p>
          {/if}

          <p class="mt-6 text-xs text-muted-foreground/50">
            Silt {settings.release} · build {settings.version}
          </p>
        </section>
      {/if}

      <!-- Sticky rather than at the bottom: a save button you have to scroll to
           find is the same complaint as a settings link you have to scroll to
           find. It only shows on the sections that can be saved. -->
      {#if !READ_ONLY_SECTIONS.has(section) && !readOnly}
        <div class="sticky bottom-0 -mx-1 flex items-center gap-3 border-t border-border bg-background/95 px-1 py-3 backdrop-blur-sm">
          <Button size="sm" onclick={save} disabled={!dirty || saving}>
            {saving ? "Saving…" : "Save changes"}
          </Button>
          {#if dirty}
            <Button variant="ghost" size="sm" onclick={revert} disabled={saving}>Discard</Button>
            <span class="text-xs text-muted-foreground">Unsaved changes</span>
          {:else}
            <span class="text-xs text-muted-foreground">
              {overridden.size === 0
                ? "Running exactly what the environment says."
                : `${overridden.size} setting${overridden.size === 1 ? "" : "s"} set here.`}
            </span>
          {/if}
        </div>
      {/if}
    {:else if !error && section !== "appearance"}
      <p class="text-sm text-muted-foreground">Loading…</p>
    {/if}
  </div>
</div>
