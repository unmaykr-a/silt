<script lang="ts">
  import SiltMark from "./SiltMark.svelte";

  let { title, hint }: { title: string; hint?: string } = $props();
</script>

<div class="rounded-md border border-dashed border-border px-6 py-12 text-center">
  <SiltMark size={28} class="mx-auto mb-3 text-muted-foreground/30" marker="currentColor" />
  <p class="text-sm text-muted-foreground">{title}</p>
  {#if hint}
    <p class="mx-auto mt-1 max-w-md text-xs leading-relaxed text-muted-foreground/70">{hint}</p>
  {/if}
</div>
