<script lang="ts">
  import { relative, absolute } from "$lib/format";

  // Every timestamp shows relative time with the absolute value on hover
  // (PROJECT.md Section 9). Timezone conversion happens here and nowhere else;
  // the server stores UTC milliseconds.
  let { ts, class: className = "" }: { ts: number; class?: string } = $props();

  // Re-render periodically so "3m ago" does not go stale on an idle page.
  let now = $state(Date.now());
  $effect(() => {
    const id = setInterval(() => (now = Date.now()), 30_000);
    return () => clearInterval(id);
  });

  const label = $derived(relative(ts, now));
</script>

<time datetime={new Date(ts).toISOString()} title={absolute(ts)} class={className}>
  {label}
</time>
