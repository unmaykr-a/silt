import { describe, it, expect } from "vitest";
import { createLocaleResolver, FALLBACK_LOCALE } from "./locale";

describe("localeFor", () => {
  it("leaves an explicit locale alone", () => {
    const localeFor = createLocaleResolver(() => {});
    expect(localeFor("en-GB")).toBe("en-GB");
    expect(localeFor("en-CA")).toBe("en-CA");
  });

  it("keeps the browser's own locale when it works", () => {
    // undefined is what tells toLocaleString to use it, so undefined is the
    // right answer here — not a locale string standing in for it.
    const localeFor = createLocaleResolver(() => {});
    expect(localeFor(undefined)).toBeUndefined();
  });

  it("stands in when the browser's locale is not a locale", () => {
    // A Chromium under LANG=en_US@posix reports `en-US@posix`, which is not a
    // valid BCP 47 tag: every toLocaleString on the page throws, and date
    // formatting is on nearly every screen, so the page goes blank.
    const localeFor = createLocaleResolver(() => {
      throw new RangeError("Invalid language tag: en-US@posix");
    });
    expect(localeFor(undefined)).toBe(FALLBACK_LOCALE);
  });

  it("does not explicitly ask for a locale it was told to use", () => {
    const localeFor = createLocaleResolver(() => {
      throw new RangeError("Invalid language tag");
    });
    // A broken system locale must not override a chosen date order.
    expect(localeFor("en-CA")).toBe("en-CA");
  });

  it("probes once, not once per timestamp", () => {
    let probes = 0;
    const localeFor = createLocaleResolver(() => {
      probes++;
    });
    for (let i = 0; i < 50; i++) localeFor(undefined);
    expect(probes).toBe(1);
  });

  it("probes once when the probe fails too", () => {
    let probes = 0;
    const localeFor = createLocaleResolver(() => {
      probes++;
      throw new RangeError("nope");
    });
    for (let i = 0; i < 50; i++) expect(localeFor(undefined)).toBe(FALLBACK_LOCALE);
    expect(probes).toBe(1);
  });
});
