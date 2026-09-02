import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { createClockCore, TICK_MS } from "./clockcore";

describe("createClockCore", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("does not run a timer with no readers", () => {
    const core = createClockCore(() => {});
    expect(core.running).toBe(false);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("runs one timer no matter how many readers there are", () => {
    // The whole point: the timeline renders hundreds of timestamps, and each
    // one used to create its own timer.
    const core = createClockCore(() => {});
    const releases = Array.from({ length: 200 }, () => core.subscribe());
    expect(core.running).toBe(true);
    expect(vi.getTimerCount()).toBe(1);
    releases.forEach((r) => r());
    expect(core.running).toBe(false);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("stops only when the last reader goes", () => {
    const core = createClockCore(() => {});
    const a = core.subscribe();
    const b = core.subscribe();
    a();
    expect(core.running).toBe(true);
    b();
    expect(core.running).toBe(false);
  });

  it("survives a release called twice", () => {
    // Svelte can run an effect's teardown more than once. Without the guard a
    // double release drives the count negative, so it never reaches zero again
    // and the timer is stranded for the life of the page.
    const core = createClockCore(() => {});
    const a = core.subscribe();
    const b = core.subscribe();
    a();
    a();
    a();
    expect(core.running).toBe(true);
    b();
    expect(core.running).toBe(false);
  });

  it("restarts cleanly after the last reader leaves", () => {
    const core = createClockCore(() => {});
    core.subscribe()();
    expect(core.running).toBe(false);
    const again = core.subscribe();
    expect(core.running).toBe(true);
    expect(vi.getTimerCount()).toBe(1);
    again();
  });

  it("ticks once per interval", () => {
    let ticks = 0;
    const core = createClockCore(() => ticks++);
    const release = core.subscribe();
    vi.advanceTimersByTime(TICK_MS * 3);
    expect(ticks).toBe(3);
    release();
    vi.advanceTimersByTime(TICK_MS * 3);
    expect(ticks).toBe(3);
  });
});
