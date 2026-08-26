<script lang="ts">
  import { api, type VersionInfo } from "$lib/api/client";
  import Dialog from "./Dialog.svelte";
  import SiltMark from "./SiltMark.svelte";

  const KOFI = "https://ko-fi.com/unmaykr";

  let info = $state<VersionInfo | null>(null);
  let open = $state(false);
  let error = $state<string | null>(null);

  $effect(() => {
    const controller = new AbortController();
    api
      .version(controller.signal)
      .then((v) => (info = v))
      .catch((err: Error) => {
        if (err.name !== "AbortError") error = err.message;
      });
    return () => controller.abort();
  });

  // The build stamp is a tag on a release and a commit otherwise, so it is the
  // honest thing to show — but `sha-b0681bd` tells nobody which release they
  // are on, so the release number leads and the build sits beside it.
  const label = $derived(info ? `v${info.release}` : "");

  const KIND_STYLE: Record<string, string> = {
    added: "bg-emerald-500/15 text-emerald-500",
    changed: "bg-sky-500/15 text-sky-500",
    fixed: "bg-amber-500/15 text-amber-500",
    security: "bg-red-500/15 text-red-500",
    removed: "bg-zinc-500/15 text-zinc-400",
  };
</script>

{#if info}
  <button
    type="button"
    class="rounded-md px-2 py-1 font-mono text-[11px] text-muted-foreground transition-colors
           hover:bg-secondary/60 hover:text-foreground"
    onclick={() => (open = true)}
    title="What's new in Silt"
  >
    {label}
  </button>

  <Dialog bind:open title="What's new in Silt">
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
                <!-- A fixed width so the text column lines up: "added" and
                     "security" are very different lengths. -->
                <span
                  class="mt-0.5 w-16 shrink-0 rounded px-1.5 py-0.5 text-center text-[10px] font-medium uppercase tracking-wide
                         {KIND_STYLE[entry.kind] ?? KIND_STYLE.removed}"
                >
                  {entry.kind}
                </span>
                <span class="min-w-0 leading-relaxed">{entry.text}</span>
              </li>
            {/each}
          </ul>
        </section>
      {/each}
    </div>
  </Dialog>
{:else if error}
  <span class="font-mono text-[11px] text-muted-foreground/50" title={error}>version unknown</span>
{/if}
