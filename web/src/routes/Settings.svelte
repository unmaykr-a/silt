<script lang="ts">
  /**
   * The settings screen's shell: the section rail, the search over it, the save
   * bar, and which panel is showing.
   *
   * Everything a section actually contains lives in $lib/settings. This file
   * was 1,586 lines with ten sections inlined into it, which meant every change
   * to Notifications was a change to the file Retention was in.
   *
   * The environment is the baseline; anything edited here is stored as an
   * override on top of it. That is why every field can say where its value came
   * from, and why "use the environment value" is a button rather than a matter
   * of typing the old number back in.
   */
  import { Button } from "$lib/components/ui/button";
  import SetupChecks from "$lib/components/SetupChecks.svelte";
  import { SECTIONS, searchSettings, overrideCounts, type SectionID } from "$lib/settingsindex";
  import { createSettingsStore } from "$lib/settings/store.svelte";
  import AppearancePanel from "$lib/settings/AppearancePanel.svelte";
  import CollectionPanel from "$lib/settings/CollectionPanel.svelte";
  import RetentionPanel from "$lib/settings/RetentionPanel.svelte";
  import NotificationsPanel from "$lib/settings/NotificationsPanel.svelte";
  import IngestPanel from "$lib/settings/IngestPanel.svelte";
  import SecurityPanel from "$lib/settings/SecurityPanel.svelte";
  import IdentityPanel from "$lib/settings/IdentityPanel.svelte";
  import EnvironmentPanel from "$lib/settings/EnvironmentPanel.svelte";
  import StoragePanel from "$lib/settings/StoragePanel.svelte";

  const store = createSettingsStore();

  // The rail reads its sections from the search index rather than declaring its
  // own list, so a section can only exist in one place and the two cannot drift
  // into disagreeing about what this screen contains.
  let section = $state<SectionID>("setup");

  // Settings search. Nine sections and forty-odd fields is past the point where
  // "it is in here somewhere" works, and the variable name is what people have
  // in hand — the compose file is where they know these from.
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

  $effect(() => {
    const controller = new AbortController();
    store.load(controller.signal);
    return () => controller.abort();
  });

  $effect(() => {
    const controller = new AbortController();
    store.loadRole(controller.signal);
    return () => controller.abort();
  });

  const settings = $derived(store.settings);
  const overridden = $derived(store.overridden);
  const counts = $derived(overrideCounts(overridden));
  // Errors and warnings from the setup review, badged on the rail so an install
  // nobody has authenticated says so from whichever section you open.
  const attention = $derived((settings?.checks ?? []).filter((c) => c.level !== "info").length);
</script>

<div class="flex flex-col gap-6 lg:flex-row">
  <!-- The section rail. Sticky rather than scrolling with the pane, so you can
       always get from Retention to Storage without scrolling back up. -->
  <nav class="shrink-0 lg:sticky lg:top-0 lg:w-56 lg:self-start" aria-label="Settings sections">
    <h2 class="mb-3 hidden text-lg font-semibold tracking-tight lg:block">Settings</h2>

    <!-- Search before the list, because with nine sections it is the faster
         route to most settings — and the only route for anyone who knows the
         setting by its environment variable rather than by which screen it was
         filed under. -->
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
      <!-- Results replace the rail rather than sitting beside it: while you are
           searching, the section list is not what you are looking at. -->
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
            <!-- How many of this section's settings are set here rather than by
                 the environment. Without it, finding your own overrides means
                 opening all nine. -->
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
        onclick={store.resetAll}
        disabled={store.saving || store.readOnly}
      >
        Use the environment for everything
      </button>
    {/if}
  </nav>

  <div class="min-w-0 flex-1 space-y-4">
    {#if store.error}
      <p class="rounded-md border border-red-500/40 bg-red-500/10 px-4 py-2.5 text-sm text-red-500 dark:text-red-300">
        {store.error}
      </p>
    {/if}
    {#if store.notice}
      <p class="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-4 py-2.5 text-sm text-emerald-600 dark:text-emerald-300">
        {store.notice}
      </p>
    {/if}

    {#if store.readOnly}
      <p class="rounded-md border border-border bg-secondary/40 px-4 py-2.5 text-sm text-muted-foreground">
        You have read-only access. Every screen is yours to read; changing Silt's own configuration
        needs an administrator. Appearance still works — those settings live in this browser, not in
        Silt.
      </p>
    {/if}

    <!-- The draft survives switching section, but the save bar does not: it only
         renders where there is something to save. So an edit made under
         Retention and then abandoned for Storage was still pending with nothing
         on screen saying so. -->
    {#if store.dirty && READ_ONLY_SECTIONS.has(section)}
      <div class="flex flex-wrap items-center gap-3 rounded-md border border-amber-500/40 bg-amber-500/10 px-4 py-2.5 text-sm">
        <span>You have unsaved changes in another section.</span>
        <Button size="sm" onclick={store.save} disabled={store.saving}>
          {store.saving ? "Saving…" : "Save them"}
        </Button>
        <Button variant="ghost" size="sm" onclick={store.revert} disabled={store.saving}>Discard</Button>
      </div>
    {/if}

    <!-- Appearance is outside the `settings` gate: it is this browser's
         preferences, so it renders before anything has loaded and still renders
         if loading failed. -->
    {#if section === "appearance"}
      <AppearancePanel />
    {/if}

    {#if settings}
      {#if section === "setup"}
        <SetupChecks checks={settings.checks} />
      {:else if section === "collection"}
        <CollectionPanel {store} />
      {:else if section === "retention"}
        <RetentionPanel {store} />
      {:else if section === "notifications"}
        <NotificationsPanel {store} />
      {:else if section === "ingest"}
        <IngestPanel {store} />
      {:else if section === "security"}
        <SecurityPanel {store} fixed={settings.fixed} />
      {:else if section === "identity"}
        <IdentityPanel id={settings.identity} />
      {:else if section === "environment"}
        <EnvironmentPanel fixed={settings.fixed} />
      {:else if section === "storage"}
        <StoragePanel {store} />
      {/if}

      <!-- Sticky rather than at the bottom: a save button you have to scroll to
           find is the same complaint as a settings link you have to scroll to
           find. It only shows on the sections that can be saved. -->
      {#if !READ_ONLY_SECTIONS.has(section) && !store.readOnly}
        <div class="sticky bottom-0 -mx-1 flex items-center gap-3 border-t border-border bg-background/95 px-1 py-3 backdrop-blur-sm">
          <Button size="sm" onclick={store.save} disabled={!store.dirty || store.saving}>
            {store.saving ? "Saving…" : "Save changes"}
          </Button>
          {#if store.dirty}
            <Button variant="ghost" size="sm" onclick={store.revert} disabled={store.saving}>Discard</Button>
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
    {:else if !store.error && section !== "appearance"}
      <p class="text-sm text-muted-foreground">Loading…</p>
    {/if}
  </div>
</div>
