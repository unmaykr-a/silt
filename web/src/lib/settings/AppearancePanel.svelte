<script lang="ts">
  /**
   * Per-viewer preferences, which is why this is the one panel that does not
   * touch the settings store: none of it is stored on the install, so two
   * people looking at the same Silt each get their own.
   */
  import Choice from "./Choice.svelte";
  import Segmented from "$lib/components/Segmented.svelte";
  import { input } from "./input";
  import { sampleDate } from "$lib/format";
  import { prefs, type Clock, type DateStyle, type Layout, type TimeStamps } from "$lib/prefs.svelte";
  import { theme, type Theme } from "$lib/theme.svelte";

  // A live sample, so a date order is chosen by looking at it rather than by
  // decoding "dmy".
  const dateSample = $derived((style: DateStyle) => sampleDate(style, prefs.clock));
</script>

<section>
  <h3 class="text-sm font-semibold">Appearance</h3>
  <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
    These live in this browser, not in Silt. A 24-hour clock or a dd/mm/yyyy date is a property of
    whoever is reading the screen, not of the install, so two people looking at the same Silt each
    get their own.
  </p>

  <div class="mt-2 divide-y divide-border">
    <Choice
      name="theme"
      label="Theme"
      hint="Silt is dark by default. &ldquo;System&rdquo; follows whatever this device is set to, which a two-way toggle cannot express — which is why the old one silently pinned you to one or the other."
    >
      <Segmented
        label="Theme"
        options={[
          { value: "light", label: "Light" },
          { value: "dark", label: "Dark" },
          { value: "system", label: "System" },
        ]}
        value={theme.value}
        onchange={(next) => theme.set(next as Theme)}
      />
    </Choice>

    <Choice
      name="layout"
      label="Navigation"
      hint="Sections across the top, or stacked in the left rail above the project list."
    >
      <div class="flex rounded-md border border-border">
        {#each [["top", "Top bar"], ["side", "Left rail"]] as [value, label] (value)}
          <button
            type="button"
            class="flex-1 px-3 py-1.5 text-xs transition-colors first:rounded-l-md last:rounded-r-md
                   {prefs.layout === value
              ? 'bg-secondary text-secondary-foreground'
              : 'text-muted-foreground hover:text-foreground'}"
            onclick={() => prefs.set("layout", value as Layout)}
          >
            {label}
          </button>
        {/each}
      </div>
    </Choice>

    <Choice name="clock" label="Clock">
      <select
        id="clock"
        class={input}
        value={prefs.clock}
        onchange={(e) => prefs.set("clock", e.currentTarget.value as Clock)}
      >
        <option value="system">Follow this device</option>
        <option value="h24">24-hour (14:30)</option>
        <option value="h12">12-hour (2:30 PM)</option>
      </select>
    </Choice>

    <Choice name="dateStyle" label="Date order">
      <select
        id="dateStyle"
        class={input}
        value={prefs.dateStyle}
        onchange={(e) => prefs.set("dateStyle", e.currentTarget.value as DateStyle)}
      >
        <option value="system">Follow this device — {dateSample("system")}</option>
        <option value="dmy">Day first — {dateSample("dmy")}</option>
        <option value="mdy">Month first — {dateSample("mdy")}</option>
        <option value="ymd">Year first — {dateSample("ymd")}</option>
      </select>
    </Choice>

    <Choice
      name="timestamps"
      label="Timestamps"
      hint="Relative reads faster on a live page; absolute is what you want when you are lining Silt up against another tool's logs. The other form is always in the tooltip."
    >
      <select
        id="timestamps"
        class={input}
        value={prefs.timestamps}
        onchange={(e) => prefs.set("timestamps", e.currentTarget.value as TimeStamps)}
      >
        <option value="relative">Relative — 3m ago</option>
        <option value="absolute">Absolute — {dateSample(prefs.dateStyle)}</option>
      </select>
      <label class="mt-2 flex items-center gap-2 text-xs text-muted-foreground">
        <input
          type="checkbox"
          checked={prefs.seconds}
          onchange={(e) => prefs.set("seconds", e.currentTarget.checked)}
          class="accent-emerald-500"
        />
        Show seconds
      </label>
    </Choice>
  </div>
</section>
