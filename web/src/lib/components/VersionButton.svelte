<script lang="ts">
  import { api, type VersionInfo } from "$lib/api/client";
  import Dialog from "./Dialog.svelte";

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
    <p class="mb-5 font-mono text-[11px] text-muted-foreground">
      release {info.release} · build {info.version}
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
                <span
                  class="mt-0.5 h-fit shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide
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
