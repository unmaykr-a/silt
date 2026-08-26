<script lang="ts">
  let { title, hint }: { title: string; hint?: string } = $props();
</script>

<div class="rounded-lg border border-dashed border-border px-6 py-10 text-center">
  <p class="text-sm text-muted-foreground">{title}</p>
  {#if hint}
    <p class="mt-1 text-xs text-muted-foreground/70">{hint}</p>
  {/if}
</div>
