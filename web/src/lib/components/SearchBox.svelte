<script lang="ts">
  import { router } from "$lib/router.svelte";

  // Navigating on every keystroke rather than on submit, so the results page
  // updates as you type and the URL is always shareable. The request behind it
  // is debounced there, not here.
  let node = $state<HTMLInputElement | null>(null);
  let value = $state("");

  const route = $derived(router.current);

  // Keep the box in step with the URL, including a back button out of a search
  // and a link into one.
  $effect(() => {
    value = route.name === "search" ? route.query : "";
  });

  function onInput(event: Event) {
    const next = (event.currentTarget as HTMLInputElement).value;
    value = next;
    const target = next.trim() === "" ? "/" : `/search?q=${encodeURIComponent(next)}`;
    // Replace rather than push: typing eight characters should not put eight
    // entries in the history for the back button to walk through.
    router.navigate(target, true);
  }

  // "/" is the search shortcut everywhere else, so it is here too — but only
  // when nothing else is taking keystrokes.
  function onKeydown(event: KeyboardEvent) {
    if (event.key !== "/" || event.metaKey || event.ctrlKey || event.altKey) return;
    const active = document.activeElement;
    const tag = active?.tagName;
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
    if (active instanceof HTMLElement && active.isContentEditable) return;
    event.preventDefault();
    node?.focus();
  }
</script>

<svelte:window onkeydown={onKeydown} />

<div class="relative">
  <svg
    class="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground/50"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    aria-hidden="true"
  >
    <circle cx="11" cy="11" r="7" />
    <path d="m20 20-3.5-3.5" />
  </svg>
  <input
    bind:this={node}
    type="search"
    {value}
    oninput={onInput}
    onkeydown={(e) => {
      if (e.key === "Escape") node?.blur();
    }}
    placeholder="Search"
    aria-label="Search projects, services, environment keys, files and events"
    class="w-32 rounded-md border border-border bg-background py-1.5 pl-7 pr-7 text-xs outline-none
           transition-[width] focus:w-56 focus:ring-2 focus:ring-ring sm:w-40"
  />
  {#if value === ""}
    <kbd
      class="pointer-events-none absolute right-1.5 top-1/2 hidden -translate-y-1/2 rounded border border-border
             px-1 font-mono text-[10px] text-muted-foreground/50 sm:block"
      aria-hidden="true"
    >
      /
    </kbd>
  {/if}
</div>
