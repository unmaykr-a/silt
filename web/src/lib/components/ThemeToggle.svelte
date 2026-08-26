<script lang="ts">
  import { theme } from "$lib/theme.svelte";

  // One SVG that morphs rather than two that swap. The sun's rays retract into
  // the disc and a shadow slides across it to carve the crescent, so the
  // transition reads as the same object changing state.
  const dark = $derived(theme.dark);
</script>

<button
  type="button"
  class="relative grid size-8 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-secondary/60 hover:text-foreground"
  onclick={() => theme.toggle()}
  aria-label={dark ? "Switch to the light theme" : "Switch to the dark theme"}
  title={dark ? "Light theme" : "Dark theme"}
>
  <svg
    width="18"
    height="18"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    aria-hidden="true"
    class="overflow-visible"
  >
    <!-- The mask is what carves the crescent: a second circle, transparent in
         the mask, slides over the disc from off to the top-right. -->
    <mask id="silt-moon-mask">
      <rect x="0" y="0" width="24" height="24" fill="white" />
      <circle
        cx="24"
        cy="10"
        r="8"
        fill="black"
        class="transition-transform duration-500 ease-in-out"
        style="transform: translateX({dark ? -8 : 0}px); transform-origin: center;"
      />
    </mask>

    <circle
      cx="12"
      cy="12"
      r={dark ? 8 : 5}
      mask="url(#silt-moon-mask)"
      class="fill-current transition-all duration-500 ease-in-out"
      stroke="none"
    />

    <g
      class="origin-center transition-all duration-500 ease-in-out"
      style="opacity: {dark ? 0 : 1}; transform: rotate({dark ? -45 : 0}deg) scale({dark ? 0.5 : 1});"
    >
      <line x1="12" y1="1" x2="12" y2="3" />
      <line x1="12" y1="21" x2="12" y2="23" />
      <line x1="1" y1="12" x2="3" y2="12" />
      <line x1="21" y1="12" x2="23" y2="12" />
      <line x1="4.2" y1="4.2" x2="5.6" y2="5.6" />
      <line x1="18.4" y1="18.4" x2="19.8" y2="19.8" />
      <line x1="4.2" y1="19.8" x2="5.6" y2="18.4" />
      <line x1="18.4" y1="5.6" x2="19.8" y2="4.2" />
    </g>
  </svg>
</button>
