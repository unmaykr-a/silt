<script lang="ts">
  import DaysField from "./DaysField.svelte";
  import IntervalField from "./IntervalField.svelte";
  import { RETENTION_INTERVALS, VACUUM_INTERVALS } from "./intervals";
  import type { SettingsStore } from "./store.svelte";

  let { store }: { store: SettingsStore } = $props();
</script>

<section>
  <h3 class="text-sm font-semibold">Retention</h3>
  <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
    Zero means keep forever. Runtime-only snapshots are the proof-of-liveness rows between changes,
    and cannot outlive the changes they sit between.
  </p>
  <div class="mt-2 divide-y divide-border">
    <DaysField {store} name="retention_days" label="Changed snapshots" envVar="SILT_RETENTION_DAYS" />
    <DaysField
      {store}
      name="unchanged_retention_days"
      label="Runtime-only snapshots"
      envVar="SILT_UNCHANGED_RETENTION_DAYS"
    />
    <DaysField {store} name="event_retention_days" label="Events" envVar="SILT_EVENT_RETENTION_DAYS" />
    <DaysField
      {store}
      name="audit_retention_days"
      label="Activity trail"
      envVar="SILT_AUDIT_RETENTION_DAYS"
      hint="Who changed Silt itself — the list under Security. A row per administrative action rather than per observation, so it stays tiny, and its whole value is how far back it reaches. 0 keeps it forever."
    />
    <IntervalField
      {store}
      name="retention_interval_ms"
      label="Retention pass runs every"
      envVar="SILT_RETENTION_INTERVAL"
      options={RETENTION_INTERVALS}
    />
    <IntervalField
      {store}
      name="vacuum_interval_ms"
      label="Vacuum"
      envVar="SILT_VACUUM_INTERVAL"
      hint="Reclaims free pages by rewriting the whole file. Cheap to skip, expensive to run."
      options={VACUUM_INTERVALS}
    />
  </div>
</section>
