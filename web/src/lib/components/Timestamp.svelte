<script lang="ts">
  import { relative, absolute, datetime } from "$lib/format";
  import { prefs } from "$lib/prefs.svelte";

  // Every timestamp shows one form with the other on hover (PROJECT.md
  // Section 9). Which one leads is the reader's choice: relative reads faster
  // on a live page, absolute is what you want when you are lining Silt up
  // against another tool's logs. Timezone conversion happens here and nowhere
  // else; the server stores UTC milliseconds.
  let { ts, class: className = "" }: { ts: number; class?: string } = $props();

  // Re-render periodically so "3m ago" does not go stale on an idle page.
  let now = $state(Date.now());
  $effect(() => {
    if (prefs.timestamps !== "relative") return;
    const id = setInterval(() => (now = Date.now()), 30_000);
    return () => clearInterval(id);
  });

  const showRelative = $derived(prefs.timestamps === "relative");
  const label = $derived(showRelative ? relative(ts, now) : datetime(ts));
  const hover = $derived(showRelative ? absolute(ts) : relative(ts, now));
</script>

<time datetime={new Date(ts).toISOString()} title={hover} class={className}>
  {label}
</time>
