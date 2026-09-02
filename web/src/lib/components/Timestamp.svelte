<script lang="ts">
  import { relative, absolute, datetime } from "$lib/format";
  import { prefs } from "$lib/prefs.svelte";
  import { clock } from "$lib/clock.svelte";

  // Every timestamp shows one form with the other on hover (PROJECT.md
  // Section 9). Which one leads is the reader's choice: relative reads faster
  // on a live page, absolute is what you want when you are lining Silt up
  // against another tool's logs. Timezone conversion happens here and nowhere
  // else; the server stores UTC milliseconds.
  let { ts, class: className = "" }: { ts: number; class?: string } = $props();

  const showRelative = $derived(prefs.timestamps === "relative");

  // Re-render periodically so "3m ago" does not go stale on an idle page. The
  // clock is shared: this component renders a few hundred times on the
  // timeline, and a few hundred private timers doing the same thing is the
  // same behaviour for a great deal more work.
  $effect(() => clock.subscribe());

  const label = $derived(showRelative ? relative(ts, clock.now) : datetime(ts));
  const hover = $derived(showRelative ? absolute(ts) : relative(ts, clock.now));
</script>

<time datetime={new Date(ts).toISOString()} title={hover} class={className}>
  {label}
</time>
