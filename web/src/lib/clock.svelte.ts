/**
 * One ticking clock for every relative timestamp on the page.
 *
 * The lifecycle lives in clockcore.ts, which has no runes and is therefore
 * testable; this is the reactive shell over it.
 */
import { createClockCore } from "./clockcore";

function createClock() {
  let now = $state(Date.now());
  const core = createClockCore(() => (now = Date.now()));

  return {
    get now() {
      return now;
    },
    /** Register a reader; call the returned function when it goes away. */
    subscribe: () => core.subscribe(),
  };
}

export const clock = createClock();
