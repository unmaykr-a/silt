<script lang="ts">
  import { api, type VersionInfo } from "$lib/api/client";
  import Dialog from "./Dialog.svelte";
  import SiltMark from "./SiltMark.svelte";
  import ChangeKind from "./ChangeKind.svelte";

  /**
   * The release history.
   *
   * Was a button plus its dialog. The button moved into the status menu, where
   * the version already appears, so this is the dialog alone and whoever opens
   * it owns the state.
   */
  const KOFI = "https://ko-fi.com/unmaykr";

  let { open = $bindable(false) }: { open?: boolean } = $props();

  let info = $state<VersionInfo | null>(null);
  let error = $state<string | null>(null);

  // Fetched when first opened rather than on mount: the changelog is a few
  // kilobytes nobody needs until they ask for it.
  $effect(() => {
    if (!open || info) return;
    const controller = new AbortController();
    api
      .version(controller.signal)
      .then((v) => (info = v))
      .catch((err: Error) => {
        if (err.name !== "AbortError") error = err.message;
      });
    return () => controller.abort();
  });
</script>

<Dialog bind:open title="What's new in Silt">
  {#if error}
    <p class="text-sm text-red-500 dark:text-red-400">{error}</p>
  {:else if !info}
    <p class="text-sm text-muted-foreground">Loading…</p>
  {:else}
    <div class="mb-5 flex flex-wrap items-center gap-3">
      <SiltMark size={20} marker="#34d399" />
      <p class="font-mono text-[11px] text-muted-foreground">
        release {info.release} · build {info.version}
      </p>
      <a
        href={KOFI}
        target="_blank"
        rel="noreferrer noopener"
        class="ml-auto inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1 text-xs
               transition-colors hover:border-foreground/25 hover:bg-secondary/50"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
             stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M18 8h1a4 4 0 0 1 0 8h-1" />
          <path d="M2 8h16v9a4 4 0 0 1-4 4H6a4 4 0 0 1-4-4Z" />
          <path d="M6 1v3M10 1v3M14 1v3" />
        </svg>
        Support on Ko-fi
      </a>
    </div>

    <p class="mb-5 text-xs leading-relaxed text-muted-foreground">
      Silt is free and AGPL-3.0 licensed. If it has saved you an evening of
      “what changed?”, a coffee is a kind way to say so — and never required.
    </p>

    <div class="space-y-7">
      {#each info.releases as release (release.version)}
        <section>
          <div class="flex items-baseline gap-3">
            <h3 class="text-sm font-semibold">{release.version}</h3>
            <span class="font-mono text-[11px] text-muted-foreground">{release.date}</span>
          </div>
          {#if release.summary}
            <p class="mt-1 text-xs text-muted-foreground">{release.summary}</p>
          {/if}
          <ul class="mt-3 space-y-2">
            {#each release.entries as entry, i (i)}
              <li class="flex gap-2.5 text-sm">
                <span class="mt-1"><ChangeKind kind={entry.kind} /></span>
                <span class="min-w-0 leading-relaxed">{entry.text}</span>
              </li>
            {/each}
          </ul>
        </section>
      {/each}
    </div>
  {/if}
</Dialog>
