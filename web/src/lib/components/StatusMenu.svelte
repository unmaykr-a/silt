<script lang="ts">
  import { api, type AuthState, type StreamStatus, type VersionInfo } from "$lib/api/client";
  import { theme, type Theme } from "$lib/theme.svelte";
  import Menu from "./Menu.svelte";
  import SiltMark from "./SiltMark.svelte";
  import Timestamp from "./Timestamp.svelte";

  /**
   * Everything the header used to spread across four controls.
   *
   * Connection state, version, theme and identity were four separate widgets
   * of four different shapes sharing one corner, all at the same visual
   * weight. None of them is something you interact with often; all of them are
   * things you occasionally want to *check*. That is a menu.
   *
   * What survives outside is one dot — the only piece that has to be visible
   * without a click, because a stale page and a live one look identical
   * otherwise.
   */
  let {
    status,
    since,
    auth,
    onSignOut,
    onShowChangelog,
  }: {
    status: StreamStatus;
    /** When the current status began, so "live" can say for how long. */
    since: number;
    auth: AuthState | null;
    onSignOut: () => void;
    onShowChangelog: () => void;
  } = $props();

  let info = $state<VersionInfo | null>(null);
  let open = $state(false);

  $effect(() => {
    const controller = new AbortController();
    api
      .version(controller.signal)
      .then((v) => (info = v))
      .catch(() => {});
    return () => controller.abort();
  });

  const dot = $derived(
    status === "live"
      ? "bg-emerald-500"
      : status === "connecting"
        ? "bg-amber-500 animate-pulse"
        : "bg-red-500",
  );

  // Say what it means, not what the state is called. "offline" alone leaves
  // the reader to guess whether the page is stale or Silt is down.
  const headline = $derived(
    status === "live"
      ? "Receiving live updates"
      : status === "connecting"
        ? "Reconnecting…"
        : "Not receiving updates",
  );
  const detail = $derived(
    status === "live"
      ? "Changes appear as they happen."
      : status === "connecting"
        ? "Retrying automatically."
        : "This page may be out of date. Reload to catch up.",
  );

  // Can Silt end this session itself? Under forward auth the proxy decides, so
  // offering a sign-out button that cannot work would be a lie.
  const canSignOut = $derived(!!auth?.required && !!auth.method && auth.method !== "proxy");

  const THEMES: { value: Theme; label: string }[] = [
    { value: "light", label: "Light" },
    { value: "dark", label: "Dark" },
    { value: "system", label: "System" },
  ];
</script>

<Menu bind:open label="Status, version and preferences" width="w-[19rem]">
  {#snippet trigger()}
    <span class="size-2 rounded-full {dot}" aria-hidden="true"></span>
    <span class="hidden text-xs sm:inline">{status === "live" ? "live" : status}</span>
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
      <path d="m6 9 6 6 6-6" />
    </svg>
  {/snippet}

  {#snippet children({ close })}
    <!-- Connection, first: it is why the dot is on screen. -->
    <div class="border-b border-border px-3 py-2.5">
      <div class="flex items-baseline gap-2">
        <span class="mt-1.5 size-2 shrink-0 self-start rounded-full {dot}" aria-hidden="true"></span>
        <div class="min-w-0">
          <p class="text-sm">{headline}</p>
          <p class="mt-0.5 text-xs text-muted-foreground">{detail}</p>
          {#if status === "live"}
            <p class="mt-0.5 text-xs text-muted-foreground/70">
              connected <Timestamp ts={since} />
            </p>
          {/if}
        </div>
      </div>
    </div>

    <!-- Theme. Three buttons rather than a toggle, because "follow the
         system" is not a state a two-way toggle can express, and pinning
         light-or-dark forever is what the toggle silently did. -->
    <div class="border-b border-border px-3 py-2.5">
      <p class="mb-1.5 text-xs text-muted-foreground">Theme</p>
      <div class="flex rounded-md border border-border" role="group" aria-label="Theme">
        {#each THEMES as option (option.value)}
          <button
            type="button"
            onclick={() => theme.set(option.value)}
            aria-pressed={theme.value === option.value}
            class="flex-1 px-2 py-1.5 text-xs transition-colors first:rounded-l-md last:rounded-r-md
                   {theme.value === option.value
              ? 'bg-secondary text-secondary-foreground'
              : 'text-muted-foreground hover:text-foreground'}"
          >
            {option.label}
          </button>
        {/each}
      </div>
    </div>

    <!-- Version. The release leads; the build stamp is what you quote in a
         bug report, so it is selectable rather than hidden in a tooltip. -->
    {#if info}
      <div class="border-b border-border px-3 py-2.5">
        <div class="flex items-center justify-between gap-3">
          <div class="flex items-center gap-2">
            <SiltMark size={16} marker="#34d399" />
            <span class="text-sm">Silt v{info.release}</span>
          </div>
          <button
            type="button"
            class="rounded-md px-2 py-1 text-xs text-muted-foreground transition-colors hover:bg-secondary/60 hover:text-foreground"
            onclick={() => {
              close();
              onShowChangelog();
            }}
          >
            What's new
          </button>
        </div>
        {#if info.version && info.version !== info.release}
          <!-- Selectable: this is the string that goes in a bug report. -->
          <p class="mt-1 text-[11px] text-muted-foreground/70">
            build <span class="select-all font-mono">{info.version}</span>
          </p>
        {/if}
      </div>
    {/if}

    <!-- Identity last: it is the least-changing thing here. -->
    <div class="px-3 py-2.5">
      {#if auth?.subject}
        <p class="truncate text-sm" title={auth.subject}>{auth.subject}</p>
        <p class="mt-0.5 text-xs text-muted-foreground">
          {auth.method === "proxy"
            ? "Identity asserted by your reverse proxy"
            : auth.method === "oidc"
              ? "Signed in with your identity provider"
              : "Signed in"}
        </p>
      {:else if auth?.required}
        <p class="text-sm text-muted-foreground">Signed in</p>
      {:else}
        <p class="text-sm text-muted-foreground">No authentication configured</p>
        <p class="mt-0.5 text-xs text-muted-foreground/70">
          Anyone who can reach this address can read it.
        </p>
      {/if}

      <div class="mt-2 flex items-center gap-2">
        <a
          href="https://ko-fi.com/unmaykr"
          target="_blank"
          rel="noreferrer noopener"
          class="rounded-md px-2 py-1 text-xs text-muted-foreground transition-colors hover:bg-secondary/60 hover:text-foreground"
        >
          Support Silt
        </a>
        {#if canSignOut}
          <button
            type="button"
            class="ml-auto rounded-md border border-border px-2 py-1 text-xs transition-colors hover:bg-secondary/60"
            onclick={() => {
              close();
              onSignOut();
            }}
          >
            Sign out
          </button>
        {/if}
      </div>
    </div>
  {/snippet}
</Menu>
