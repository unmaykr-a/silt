import { describe, it, expect } from "vitest";
import { sanitiseTag, sanitiseLocales, guarded, guardIntl, FALLBACK_TAG } from "./localeguard";

describe("sanitiseTag", () => {
  it("passes a valid tag straight through", () => {
    expect(sanitiseTag("en-GB")).toBe("en-GB");
    expect(sanitiseTag("pt-BR")).toBe("pt-BR");
    expect(sanitiseTag("et")).toBe("et");
  });

  it("drops a POSIX modifier", () => {
    // What a browser under LANG=C or LANG=POSIX actually reports.
    expect(sanitiseTag("en-US@posix")).toBe("en-US");
  });

  it("gives up on something with nothing to salvage", () => {
    expect(sanitiseTag("!!!")).toBeNull();
    expect(sanitiseTag("")).toBeNull();
    expect(sanitiseTag(undefined)).toBeNull();
    expect(sanitiseTag(42)).toBeNull();
  });
});

describe("sanitiseLocales", () => {
  it("names a tag when the browser's own default is the broken one", () => {
    // undefined means "use the browser's locale", so a throw on undefined is
    // that locale being unusable. Passing undefined again would throw again.
    expect(sanitiseLocales(undefined)).toBe(FALLBACK_TAG);
    expect(sanitiseLocales(null)).toBe(FALLBACK_TAG);
  });

  it("repairs a single tag", () => {
    expect(sanitiseLocales("en-US@posix")).toBe("en-US");
  });

  it("keeps the usable entries of a list", () => {
    expect(sanitiseLocales(["en-US@posix", "!!!", "fr"])).toEqual(["en-US", "fr"]);
  });

  it("falls back when a list has nothing usable in it", () => {
    expect(sanitiseLocales(["!!!"])).toBe(FALLBACK_TAG);
    expect(sanitiseLocales([])).toBe(FALLBACK_TAG);
  });
});

describe("guarded", () => {
  /** A constructor that throws on exactly the tag a POSIX browser reports. */
  class Picky {
    locale: unknown;
    constructor(locales?: unknown) {
      if (locales === undefined || locales === "en-US@posix") {
        throw new RangeError("Invalid language tag: en-US@posix");
      }
      this.locale = locales;
    }
  }

  it("retries with a usable locale instead of throwing", () => {
    const Guarded = guarded(Picky as never) as unknown as typeof Picky;
    expect(new Guarded("en-US@posix").locale).toBe("en-US");
    // undefined is the browser's own locale being the broken one.
    expect(new Guarded().locale).toBe(FALLBACK_TAG);
  });

  it("leaves a working construction untouched", () => {
    const Guarded = guarded(Picky as never) as unknown as typeof Picky;
    expect(new Guarded("en-GB").locale).toBe("en-GB");
  });

  it("keeps instanceof working", () => {
    const Guarded = guarded(Picky as never) as unknown as typeof Picky;
    expect(new Guarded("en-GB")).toBeInstanceOf(Picky);
  });

  it("still throws on something that is not a locale problem", () => {
    class Broken {
      constructor() {
        throw new TypeError("options is not an object");
      }
    }
    const Guarded = guarded(Broken as never) as unknown as typeof Broken;
    expect(() => new Guarded()).toThrow(TypeError);
  });

  it("does not swallow a second failure", () => {
    class Always {
      constructor() {
        throw new RangeError("nope");
      }
    }
    const Guarded = guarded(Always as never) as unknown as typeof Always;
    expect(() => new Guarded()).toThrow(RangeError);
  });
});

describe("guardIntl", () => {
  it("survives an Intl with nothing on it", () => {
    // Belt and braces: the guard runs at import, before anything else, and
    // must not be the thing that breaks the page.
    expect(() => guardIntl({} as typeof Intl)).not.toThrow();
  });

  it("leaves a real formatter working", () => {
    // The guard has already run against the real Intl at import.
    expect(new Intl.NumberFormat("en-GB").format(1234.5)).toBe("1,234.5");
    expect(new Intl.NumberFormat("en-US@posix").format(1)).toBe("1");
  });
});
