<script lang="ts">
  import { severityDot } from "$lib/format";

  let { severity, label }: { severity: string; label?: string } = $props();
</script>

<span class="inline-flex items-center gap-1.5 text-xs">
  <span class="size-1.5 rounded-full {severityDot(severity)}" aria-hidden="true"></span>
  <span class="text-muted-foreground">{label ?? severity}</span>
</span>
