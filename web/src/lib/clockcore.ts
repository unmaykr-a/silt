/**
 * The timer behind every relative timestamp, with no runes in it.
 *
 * Split from the rune module deliberately. Silt's vitest config leaves out the
 * Svelte plugin — see PROJECT.md Section 15 — so a `.svelte.ts` module cannot
 * be imported by a test. The reference counting and the timer lifecycle are
 * the parts worth testing, so they live here, and `clock.svelte.ts` is the
 * thin reactive shell over them.
 */

/** How often "3m ago" is recomputed. Below a minute nothing else moves. */
export const TICK_MS = 30_000;

export type ClockCore = {
  subscribe(): () => void;
  readonly running: boolean;
};

/**
 * Reference-counted ticker.
 *
 * Only one timer runs no matter how many readers there are: the timeline
 * renders a few hundred timestamps, and a few hundred timers doing the same
 * thing is the same behaviour for a great deal more work — and they wake on
 * their own phases, so the page updates as a ripple rather than at once.
 *
 * It stops when the last reader goes, so a tab parked on a screen with no
 * relative timestamps does not wake up twice a minute forever.
 */
export function createClockCore(onTick: () => void): ClockCore {
  let readers = 0;
  let timer: ReturnType<typeof setInterval> | null = null;

  return {
    subscribe() {
      readers++;
      if (timer === null) timer = setInterval(onTick, TICK_MS);

      // Svelte can run an effect's teardown more than once. Without this
      // guard a double release drives the count negative and the timer is
      // stranded: it never reaches zero again and never stops.
      let released = false;
      return () => {
        if (released) return;
        released = true;
        readers = Math.max(0, readers - 1);
        if (readers === 0 && timer !== null) {
          clearInterval(timer);
          timer = null;
        }
      };
    },
    get running() {
      return timer !== null;
    },
  };
}
