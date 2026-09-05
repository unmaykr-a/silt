<script lang="ts">
  /**
   * A setting Silt reports but does not let you change.
   *
   * There were three of these, defined separately in Security and in
   * Authentication and drifting apart: the hint sat under the value in one and
   * under the label in the other, so two lists of the same kind of thing did
   * not look like it. The label's version won — a hint explains what the
   * setting is, and the value is the answer, so putting prose in the answer
   * column makes the column you are scanning ragged.
   */
  let {
    label,
    value,
    envVar = "",
    hint,
  }: {
    label: string;
    value: string;
    /** Empty for a setting with no variable behind it. */
    envVar?: string;
    hint?: string;
  } = $props();
</script>

<div class="grid gap-1 py-2.5 text-sm sm:grid-cols-[15rem_1fr] sm:gap-6">
  <dt class="min-w-0">
    {label}
    {#if hint}
      <span class="mt-0.5 block text-xs leading-relaxed text-muted-foreground/70">{hint}</span>
    {/if}
    {#if envVar}
      <span class="mt-0.5 block font-mono text-[10px] text-muted-foreground/40">{envVar}</span>
    {/if}
  </dt>
  <dd class="min-w-0 break-all font-mono text-xs {value === 'not set' ? 'text-muted-foreground/50' : ''}">
    {value}
  </dd>
</div>
