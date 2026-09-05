<script lang="ts">
  /**
   * Row, where the value is on or off.
   *
   * A read-only Toggle rather than the words: a column of pills is scannable
   * in a way a column of "true"/"false" is not, which is the whole argument for
   * the Toggle component existing.
   */
  import Toggle from "$lib/components/Toggle.svelte";

  let {
    label,
    on,
    envVar = "",
    hint,
  }: { label: string; on: boolean; envVar?: string; hint?: string } = $props();
</script>

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
  <dd class="min-w-0"><Toggle checked={on} readonly {label} /></dd>
</div>
