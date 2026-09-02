<script lang="ts">
  import type { Snippet } from "svelte";

  /**
   * A dropdown anchored to its trigger.
   *
   * The header used to be five separate controls — search, a status dot, a
   * version button, a theme toggle and a sign-out button — each a different
   * shape, all at the same visual weight, competing for the same corner. Most
   * of them are things you look at rarely and change even more rarely, which
   * is what a menu is for.
   *
   * Deliberately small: no portal, no floating-element library. The header is
   * the only place this opens from, and it opens downward against a fixed bar.
   */
  let {
    label,
    align = "end",
    width = "w-72",
    trigger,
    children,
    open = $bindable(false),
  }: {
    label: string;
    align?: "start" | "end";
    width?: string;
    trigger: Snippet<[{ open: boolean }]>;
    children: Snippet<[{ close: () => void }]>;
    open?: boolean;
  } = $props();

  let root = $state<HTMLDivElement | null>(null);

  function close() {
    open = false;
  }

  // Closing on an outside click and on Escape is what makes a menu feel like a
  // menu rather than a panel you have to click the button again to dismiss.
  $effect(() => {
    if (!open) return;

    const onPointer = (e: PointerEvent) => {
      if (root && !root.contains(e.target as Node)) close();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        close();
        // Return focus to the trigger, or the next Tab starts from the top of
        // the document.
        root?.querySelector<HTMLElement>("button")?.focus();
      }
    };
    // Capture, so a click on a control that stops propagation still closes.
    document.addEventListener("pointerdown", onPointer, true);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onPointer, true);
      document.removeEventListener("keydown", onKey);
    };
  });
</script>

<div class="relative" bind:this={root}>
  <button
    type="button"
    onclick={() => (open = !open)}
    aria-haspopup="menu"
    aria-expanded={open}
    aria-label={label}
    class="flex items-center gap-1.5 rounded-md px-2 py-1 text-muted-foreground transition-colors
           hover:bg-secondary/60 hover:text-foreground
           {open ? 'bg-secondary text-foreground' : ''}"
  >
    {@render trigger({ open })}
  </button>

  {#if open}
    <!-- max-w keeps it inside the viewport on a phone: the trigger sits at the
         right edge of the header, so a fixed width wider than the gap to the
         left edge hangs off the screen. -->
    <div
      role="menu"
      tabindex="-1"
      class="absolute top-full z-50 mt-1.5 {width} max-w-[calc(100vw-1.5rem)] overflow-hidden
             rounded-lg border border-border bg-popover text-popover-foreground shadow-lg
             {align === 'end' ? 'right-0' : 'left-0'}"
    >
      {@render children({ close })}
    </div>
  {/if}
</div>
