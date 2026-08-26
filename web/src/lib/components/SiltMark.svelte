<script lang="ts">
  // Silt's mark: strata, and the moment one layer was marked.
  //
  // Layers are what Silt draws and the dot is the change it recorded. The dot
  // is also what keeps it from reading as a hamburger menu at 16px, which is
  // the whole reason the plain three-line version was not chosen.
  let {
    size = 18,
    class: klass = "",
    marker = "currentColor",
  }: { size?: number; class?: string; marker?: string } = $props();
</script>

<svg
  width={size}
  height={size}
  viewBox="0 0 24 24"
  aria-hidden="true"
  class="shrink-0 {klass}"
>
  <g fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
    <path d="M3 7h18" opacity="0.4" />
    <path d="M3 12h18" opacity="0.75" />
    <path d="M3 17h18" opacity="0.4" />
  </g>
  <circle cx="16" cy="12" r="3" fill={marker === "currentColor" ? "currentColor" : marker} />
</svg>
