/**
 * Where a sliding selection marker should sit.
 *
 * Extracted from the components so the rule can be tested: the vitest config
 * has no Svelte plugin, so a `.svelte` file's logic is only reachable through
 * a browser. This is the part that had a bug worth pinning.
 */

export type MarkerBox = { left: number; width: number; ready: boolean };

/** Nothing is selected. */
export const HIDDEN: MarkerBox = { left: 0, width: 0, ready: false };

/**
 * Measure the marker from the active element, or hide it when there is none.
 *
 * Deliberately takes the measurements rather than an element, and returns a
 * fresh object rather than merging into the previous one.
 *
 * The merge is what broke it. `{ ...current, ready: false }` *reads* the
 * current value, and the callers run this synchronously inside the effect that
 * writes the result — so the effect took a dependency on its own output and
 * re-ran until Svelte gave up with effect_update_depth_exceeded. It only
 * showed on routes where nothing is active (/search, /not-found), because
 * that was the only branch that merged.
 */
export function markerFor(active: { offsetLeft: number; offsetWidth: number } | null): MarkerBox {
  if (!active) return { ...HIDDEN };
  return { left: active.offsetLeft, width: active.offsetWidth, ready: true };
}
