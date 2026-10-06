<script lang="ts">
  /**
   * One event, with enough around it to act on.
   *
   * The feed row it opens from said `03:00 container.die` and stopped there,
   * which is the wrong amount of information at the one moment someone is
   * looking: enough to know something happened, not enough to do anything.
   *
   * So three things, in the order they are wanted. What it was — including the
   * exit code, which the capture path used to discard. What changed just before
   * it, because that is the question Silt exists to answer and it was
   * unreachable from here. And what else was happening, because a container
   * that died alone and one that died in the middle of a restart loop are
   * different problems.
   */
  import Dialog from "./Dialog.svelte";
  import { api, type EventDetail } from "$lib/api/client";
  import { link } from "$lib/router.svelte";
  import { clockTime, datetime, relative, duration, severityDot } from "$lib/format";

  let {
    eventId = $bindable(),
  }: {
    /** The event to show, or null for closed. */
    eventId: number | null;
  } = $props();

  let detail = $state<EventDetail | null>(null);
  let error = $state<string | null>(null);
  let copied = $state(false);


  $effect(() => {
    const id = eventId;
    if (id === null) {
      detail = null;
      error = null;
      copied = false;
      return;
    }
    const controller = new AbortController();
    api
      .event(id, controller.signal)
      .then((d) => {
        detail = d;
        error = null;
      })
      .catch((err) => {
        if ((err as Error).name !== "AbortError") error = (err as Error).message;
      });
    return () => controller.abort();
  });

  // The engine's own attributes, which the capture path keeps under their own
  // keys. Split out from the three Silt writes itself so the panel can lead
  // with the ones that answer the question.
  const SILT_KEYS = new Set(["project", "image", "action", "withheld_attributes"]);

  type Field = { key: string; value: string };

  const payload = $derived.by((): Record<string, unknown> => {
    const raw = detail?.event?.payload;
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) return {};
    return raw as Record<string, unknown>;
  });

  const engineFields = $derived.by((): Field[] =>
    Object.entries(payload)
      .filter(([k]) => !SILT_KEYS.has(k))
      .map(([key, value]) => ({ key, value: String(value) }))
      .sort((a, b) => a.key.localeCompare(b.key)),
  );

  const withheld = $derived(Number(payload.withheld_attributes ?? 0));

  // The whole event as text, for pasting into a bug report or handing to
  // something that can read it. The panel is for a person; this is for
  // everything else they might want to do with it.
  const asText = $derived.by(() => {
    if (!detail) return "";
    const e = detail.event;
    const lines = [
      `event      ${e.type}`,
      `at         ${datetime(e.ts, { seconds: true })}`,
      `severity   ${e.severity}`,
      `source     ${e.source}`,
    ];
    if (detail.project) lines.push(`project    ${detail.project.name}`);
    if (e.service) lines.push(`service    ${e.service}`);
    if (detail.host) lines.push(`host       ${detail.host}`);
    if (e.actor) lines.push(`actor      ${e.actor}`);
    if (e.message) lines.push(`message    ${e.message}`);
    for (const f of engineFields) lines.push(`${f.key.padEnd(10)} ${f.value}`);
    if (withheld > 0) lines.push(`withheld   ${withheld} container labels, not recorded`);
    if (detail.previous_change) {
      lines.push(
        `previous change  snapshot ${detail.previous_change.snapshot_id}, ` +
          `${duration(detail.previous_change.before_ms)} before this`,
      );
    }
    if (detail.nearby.length > 0) {
      lines.push("", `nearby (${detail.nearby.length} events in this window)`);
      for (const n of detail.nearby) {
        lines.push(`  ${datetime(n.ts, { seconds: true })}  ${n.type}  ${n.service ?? ""} ${n.message ?? ""}`.trimEnd());
      }
    }
    return lines.join("\n");
  });

  async function copy() {
    try {
      await navigator.clipboard.writeText(asText);
      copied = true;
      setTimeout(() => (copied = false), 1600);
    } catch {
      // No clipboard permission. The text is on screen either way.
    }
  }
</script>

<!--
  Bound both ways, through a getter and a setter, because eventId is the only
  state here and the dialog has to be able to give it back.

  Passing `open` one-way looked right and broke on the second open: Dialog takes
  `open` as $bindable and sets it false when the element closes, so without a
  binding that write landed on its own copy. eventId stayed set, this side kept
  computing open as true, the prop never changed value — and the effect that
  calls showModal never saw an edge again. One event opened per page load.
-->
<Dialog
  bind:open={
    () => eventId !== null,
    (next) => {
      if (!next) eventId = null;
    }
  }
  title={detail ? detail.event.type : "Event"}
  class="max-w-3xl"
>
  <div class="max-h-[70vh] overflow-y-auto px-5 py-4">
    {#if error}
      <p class="rounded-md border border-red-500/40 bg-red-500/10 px-3 py-2 text-sm text-red-500 dark:text-red-300">
        {error}
      </p>
    {:else if !detail}
      <p class="text-sm text-muted-foreground">Loading…</p>
    {:else}
      {@const e = detail.event}
      <div class="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <span class="inline-flex items-center gap-1.5 text-sm">
          <span class="size-1.5 rounded-full {severityDot(e.severity)}"></span>
          {e.severity}
        </span>
        <span class="font-mono text-sm">{e.type}</span>
        <span class="text-xs text-muted-foreground" title={datetime(e.ts, { seconds: true })}>
          {datetime(e.ts, { seconds: true })} · {relative(e.ts)}
        </span>
      </div>

      <!-- What it was. Everything the row had room for and everything it did
           not, in one place, so nothing has to be guessed from the type. -->
      <dl class="mt-4 grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[8rem_1fr]">
        {#if detail.project}
          <dt class="text-muted-foreground">Project</dt>
          <dd class="min-w-0">
            <a use:link href="/projects/{detail.project.id}" class="underline-offset-4 hover:underline">
              {detail.project.name}
            </a>
          </dd>
        {/if}
        {#if e.service}
          <dt class="text-muted-foreground">Service</dt>
          <dd class="min-w-0">
            {#if detail.project}
              <a
                use:link
                href="/projects/{detail.project.id}/services/{e.service}"
                class="underline-offset-4 hover:underline"
              >
                {e.service}
              </a>
            {:else}
              {e.service}
            {/if}
          </dd>
        {/if}
        <dt class="text-muted-foreground">Source</dt>
        <dd class="min-w-0">{e.source}</dd>
        {#if e.message}
          <dt class="text-muted-foreground">Message</dt>
          <dd class="min-w-0 break-words font-mono text-xs">{e.message}</dd>
        {/if}
        {#each engineFields as f (f.key)}
          <dt class="text-muted-foreground">{f.key.replace(/_/g, " ")}</dt>
          <dd class="min-w-0 break-words font-mono text-xs">{f.value}</dd>
        {/each}
        {#if e.actor}
          <dt class="text-muted-foreground">Container</dt>
          <dd class="min-w-0 break-all font-mono text-xs">{e.actor}</dd>
        {/if}
        {#if detail.host}
          <dt class="text-muted-foreground">Host</dt>
          <dd class="min-w-0">{detail.host}</dd>
        {/if}
      </dl>

      {#if withheld > 0}
        <!-- Said rather than omitted: a panel that showed four attributes
             without mentioning the other twelve would imply this is everything
             the engine sent. -->
        <p class="mt-3 text-xs text-muted-foreground/70">
          The engine sent {withheld} more {withheld === 1 ? "attribute" : "attributes"} — the container's
          labels. Silt does not record them here, because labels hold secrets often enough that keeping
          them in every event would be the wrong default. They are captured, redacted, in each snapshot's
          own label set.
        </p>
      {/if}

      <!-- What changed before it. The reason this panel exists at all. -->
      <h3 class="mt-6 text-xs font-medium uppercase tracking-wide text-muted-foreground">
        Before this
      </h3>
      {#if detail.previous_change}
        <a
          use:link
          href="/diff?to={detail.previous_change.snapshot_id}&project={detail.previous_change.project_id}"
          class="mt-2 flex flex-wrap items-baseline gap-x-2 gap-y-1 rounded-md border border-border
                 bg-secondary/30 px-3 py-2 text-sm transition-colors hover:bg-secondary/60"
        >
          <span class="font-medium">configuration changed</span>
          <span class="text-xs text-muted-foreground">
            {duration(detail.previous_change.before_ms)} before this event
          </span>
          <span class="ml-auto text-xs text-muted-foreground/60">open the diff</span>
        </a>
      {:else}
        <p class="mt-2 text-sm text-muted-foreground">
          {#if detail.project}
            Nothing changed in this project before the event, as far back as the retained history goes.
          {:else}
            This event is not linked to a project, so there is no configuration history to compare it to.
          {/if}
        </p>
      {/if}

      <!-- What else was happening. One died alone and one died in a restart
           loop are different problems. -->
      <h3 class="mt-6 flex items-baseline gap-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
        Around it
        <span class="font-normal normal-case tracking-normal text-muted-foreground/60">
          {duration(detail.window_to - detail.window_from)} window{detail.project ? ", this project" : ""}
        </span>
      </h3>
      {#if detail.nearby.length === 0}
        <p class="mt-2 text-sm text-muted-foreground">Nothing else, which is its own answer.</p>
      {:else}
        <ul class="mt-2 divide-y divide-border/60">
          {#each detail.nearby as n (n.id)}
            <li class="flex items-baseline gap-3 py-1.5 text-xs">
              <span class="size-1.5 shrink-0 rounded-full {severityDot(n.severity)}"></span>
              <span
                class="w-16 shrink-0 font-mono tabular-nums text-muted-foreground"
                title={datetime(n.ts, { seconds: true })}
              >
                {clockTime(n.ts, true)}
              </span>
              <button
                type="button"
                class="shrink-0 font-mono underline-offset-4 hover:underline"
                onclick={() => (eventId = n.id)}
              >
                {n.type}
              </button>
              {#if n.service}
                <span class="shrink-0 text-muted-foreground">{n.service}</span>
              {/if}
              {#if n.message}
                <span class="min-w-0 flex-1 truncate text-muted-foreground/70">{n.message}</span>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    {/if}
  </div>

  {#if detail}
    <div class="flex flex-wrap items-center gap-2 border-t border-border px-5 py-3">
      <button
        type="button"
        class="rounded-md border border-border px-2.5 py-1 text-xs transition-colors hover:bg-secondary/60"
        onclick={copy}
      >
        {copied ? "Copied" : "Copy as text"}
      </button>
      <span class="text-[11px] text-muted-foreground/60">
        Everything above, as plain text to paste somewhere that can read it.
      </span>
    </div>
  {/if}
</Dialog>
