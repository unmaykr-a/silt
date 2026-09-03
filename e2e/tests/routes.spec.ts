import { test, expect, type Page } from "@playwright/test";

/**
 * Every route, at every width, in every configuration.
 *
 * The bug this was written for: the sliding marker under the section links
 * cleared itself by merging into its own previous value, which loops the
 * effect and kills the page. It only fired on /search and an unknown URL —
 * the two routes where no section is active — so every screenshot of a
 * working screen looked fine.
 */

const ROUTES = [
  { path: "/", name: "timeline" },
  { path: "/projects", name: "projects" },
  { path: "/projects/1", name: "project" },
  { path: "/projects/1/services/radarr", name: "service" },
  { path: "/projects/1/files", name: "files" },
  { path: "/diff?from=1&to=5&project=1", name: "diff" },
  // Nothing in the section nav matches these two. That is the point.
  { path: "/search?q=radarr", name: "search" },
  { path: "/no/such/page", name: "not found" },
  { path: "/settings", name: "settings" },
];

const SIZES = [
  { width: 1600, height: 1000, name: "desktop" },
  { width: 1024, height: 768, name: "tablet" },
  { width: 390, height: 844, name: "phone" },
  // The narrowest width in common use. If the header fits here it fits.
  { width: 320, height: 568, name: "small phone" },
];

/** Console errors and uncaught exceptions, collected for the whole page. */
function watchForErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(`uncaught: ${e.message}`));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(`console: ${m.text()}`);
  });
  return errors;
}

async function configure(page: Page, theme: string, layout: string) {
  await page.goto("/", { waitUntil: "domcontentloaded" });
  await page.evaluate(
    ([t, l]) => {
      localStorage.setItem("silt.theme", t);
      const prefs = JSON.parse(localStorage.getItem("silt.prefs") ?? "{}");
      prefs.layout = l;
      localStorage.setItem("silt.prefs", JSON.stringify(prefs));
    },
    [theme, layout],
  );
}

for (const { theme, layout } of [
  { theme: "dark", layout: "top" },
  { theme: "light", layout: "top" },
  { theme: "dark", layout: "side" },
]) {
  test.describe(`${theme} theme, ${layout} layout`, () => {
    for (const size of SIZES) {
      for (const route of ROUTES) {
        test(`${route.name} at ${size.name}`, async ({ page }) => {
          const errors = watchForErrors(page);
          await page.setViewportSize({ width: size.width, height: size.height });
          await configure(page, theme, layout);

          await page.goto(route.path, { waitUntil: "domcontentloaded" });
          await page.waitForTimeout(700);

          expect(errors, `${route.path} logged errors`).toEqual([]);

          // The page rendered something.
          const text = (await page.locator("body").innerText()).trim();
          expect(text.length, `${route.path} rendered nothing`).toBeGreaterThan(0);

          // Nothing sticks out past the viewport. A header that overflows is
          // invisible until a menu opens and scrolls the page sideways.
          const overflow = await page.evaluate(
            () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
          );
          expect(overflow, `${route.path} scrolls horizontally`).toBeLessThanOrEqual(1);
        });
      }
    }
  });
}
