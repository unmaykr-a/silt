import { describe, it, expect } from "vitest";
import { markerFor, HIDDEN } from "./marker";

describe("markerFor", () => {
  it("measures from the active element", () => {
    expect(markerFor({ offsetLeft: 40, offsetWidth: 90 })).toEqual({
      left: 40,
      width: 90,
      ready: true,
    });
  });

  it("hides when nothing is selected", () => {
    // /search and /not-found match no section; the timeline matches no range
    // while a window is dragged out on the density strip.
    expect(markerFor(null)).toEqual(HIDDEN);
  });

  it("returns a fresh object every time", () => {
    // The bug: merging into the previous value reads it, and the callers run
    // this inside the effect that writes the result — so the effect depended
    // on its own output and looped until Svelte aborted the page.
    const a = markerFor(null);
    const b = markerFor(null);
    expect(a).not.toBe(b);
    expect(a).not.toBe(HIDDEN);
    a.left = 999;
    expect(HIDDEN.left).toBe(0);
    expect(markerFor(null).left).toBe(0);
  });

  it("does not carry anything over from a previous measurement", () => {
    const measured = markerFor({ offsetLeft: 40, offsetWidth: 90 });
    const hidden = markerFor(null);
    expect(hidden.width).toBe(0);
    expect(hidden.ready).toBe(false);
    expect(measured.ready).toBe(true);
  });
});
