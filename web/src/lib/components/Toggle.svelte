<script lang="ts">
  /**
   * An on/off control that says which it is.
   *
   * A bare checkbox states its value only by being filled or not, which is a
   * two-pixel difference read at a glance across a screen of thirty settings —
   * and on a settings page the question is almost always "is this on?" rather
   * than "turn this on". So the state is a word, in the colour of its meaning,
   * and the whole pill is the target.
   *
   * Deliberately not a sliding switch: a switch has the same problem as the
   * checkbox — left and right mean nothing until you know which end is on —
   * and needs a label beside it anyway to be legible.
   */
  let {
    checked = $bindable(),
    label,
    onchange,
    disabled = false,
    readonly = false,
  }: {
    checked: boolean;
    /** Names what is being toggled, for anyone not reading the layout. */
    label: string;
    onchange?: (next: boolean) => void;
    disabled?: boolean;
    /** Shows the state without offering to change it. */
    readonly?: boolean;
  } = $props();

  function toggle() {
    if (disabled || readonly) return;
    checked = !checked;
    onchange?.(checked);
  }
</script>

<button
  type="button"
  role="switch"
  aria-checked={checked}
  aria-label={label}
  aria-readonly={readonly || undefined}
  {disabled}
  onclick={toggle}
  class="inline-flex shrink-0 items-center gap-1.5 rounded-md border px-2 py-1 text-xs font-medium transition-colors
         disabled:opacity-50
         {checked
    ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
    : 'border-border bg-secondary/40 text-muted-foreground'}
         {readonly ? 'cursor-default' : 'hover:brightness-110'}"
>
  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"
       stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    {#if checked}
      <path d="m5 13 4 4L19 7" />
    {:else}
      <circle cx="12" cy="12" r="9" stroke-width="2" />
      <path d="m6 6 12 12" stroke-width="2" />
    {/if}
  </svg>
  {checked ? "ON" : "OFF"}
</button>
