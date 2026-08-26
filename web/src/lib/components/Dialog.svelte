<script lang="ts">
  import type { Snippet } from "svelte";

  // A dialog built on <dialog> rather than a portal-and-focus-trap component.
  // The element already does the modal work — focus containment, Escape, the
  // top layer — and Silt needs two of these, not a dialog system.
  let {
    open = $bindable(false),
    title,
    children,
    class: klass = "max-w-2xl",
  }: {
    open?: boolean;
    title: string;
    children: Snippet;
    class?: string;
  } = $props();

  let node = $state<HTMLDialogElement | null>(null);

  $effect(() => {
    if (!node) return;
    if (open && !node.open) node.showModal();
    if (!open && node.open) node.close();
  });
</script>

<dialog
  bind:this={node}
  onclose={() => (open = false)}
  onclick={(e) => {
    // A click on the backdrop lands on the dialog element itself; a click on
    // anything inside lands on a child.
    if (e.target === node) open = false;
  }}
  class="m-auto w-[calc(100vw-2rem)] {klass} rounded-lg border border-border bg-background p-0
         text-foreground shadow-xl backdrop:bg-black/50 backdrop:backdrop-blur-sm"
>
  <div class="flex items-center justify-between gap-4 border-b border-border px-5 py-3">
    <h2 class="text-sm font-semibold tracking-tight">{title}</h2>
    <button
      type="button"
      class="rounded-md p-1 text-muted-foreground transition-colors hover:bg-secondary/60 hover:text-foreground"
      onclick={() => (open = false)}
      aria-label="Close"
    >
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M18 6 6 18M6 6l12 12" />
      </svg>
    </button>
  </div>
  <div class="max-h-[70vh] overflow-y-auto px-5 py-4">
    {@render children()}
  </div>
</dialog>
