import { test, expect, type Page } from "@playwright/test";

/** Console errors and uncaught exceptions for the whole page. */
function watchForErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(`uncaught: ${e.message}`));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(`console: ${m.text()}`);
  });
  return errors;
}

test.beforeEach(async ({ page }) => {
  await page.goto("/", { waitUntil: "domcontentloaded" });
  await page.evaluate(() => localStorage.setItem("silt.theme", "dark"));
});

test("every time range redraws the timeline", async ({ page }) => {
  const errors = watchForErrors(page);
  await page.goto("/", { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(900);

  for (const range of ["1h", "6h", "24h", "7d", "30d"]) {
    await page.click(`div[role="group"][aria-label="Time range"] button:has-text("${range}")`);
    await page.waitForTimeout(400);
    await expect(page.locator("canvas").first()).toBeVisible();
  }
  expect(errors).toEqual([]);
});

test("the selection marker slides rather than jumping", async ({ page }) => {
  await page.goto("/projects", { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(900);

  const marker = page.locator('div[role="group"][aria-label="Sort projects"] span[aria-hidden="true"]');
  const before = await marker.evaluate((m) => (m as HTMLElement).style.left);
  await page.click('div[role="group"][aria-label="Sort projects"] button:has-text("Name")');
  // Sampled mid-transition: an element that jumps is already at its
  // destination by now, one that animates is somewhere in between.
  await page.waitForTimeout(90);
  const during = await marker.evaluate((m) => getComputedStyle(m).left);
  await page.waitForTimeout(400);
  const after = await marker.evaluate((m) => (m as HTMLElement).style.left);

  expect(after).not.toBe(before);
  expect(during).not.toBe(after);
});

test("the section marker follows a drill-down", async ({ page }) => {
  // A service page is still Projects. Losing that means the nav stops telling
  // you where you are as soon as you go anywhere.
  await page.goto("/projects/1/services/radarr", { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(800);
  await expect(page.locator('nav[aria-label="Sections"] [aria-current="page"]')).toHaveAttribute(
    "aria-label",
    "Projects",
  );
});

test("each attention filter narrows the grid without breaking it", async ({ page }) => {
  const errors = watchForErrors(page);
  await page.goto("/projects", { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(900);

  const chips = page.locator("div.flex-wrap > button");
  const count = await chips.count();
  expect(count, "the demo host should trip several attention filters").toBeGreaterThan(2);

  for (let i = 0; i < count; i++) {
    await chips.nth(i).click();
    await page.waitForTimeout(250);
    // Filtering to nothing is a legitimate outcome; a crash is not.
    expect(errors).toEqual([]);
    await chips.nth(i).click();
    await page.waitForTimeout(150);
  }
});

test("a filter matching nothing says so", async ({ page }) => {
  await page.goto("/projects", { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(800);
  await page.fill('input[aria-label="Filter projects"]', "no-such-project-anywhere");
  await page.waitForTimeout(400);
  await expect(page.locator("body")).toContainText("Nothing matches");
});

test("both diff views render", async ({ page }) => {
  const errors = watchForErrors(page);
  await page.goto("/diff?from=1&to=5&project=1", { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(900);
  for (const view of ["Structured", "YAML"]) {
    await page.click(`button:has-text("${view}")`);
    await page.waitForTimeout(500);
  }
  expect(errors).toEqual([]);
});

test("every settings section renders", async ({ page }) => {
  const errors = watchForErrors(page);
  await page.goto("/settings", { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(900);

  // Read the sections off the rail rather than listing them here. A hardcoded
  // copy had already gone stale twice — it was missing Setup and
  // Authentication within a release of their being added — and a section that
  // nothing opens is a section nothing checks.
  const rail = page.getByLabel("Settings sections");
  const buttons = rail.getByRole("button");
  const count = await buttons.count();
  expect(count).toBeGreaterThanOrEqual(9);

  // By index rather than by name: a section with overrides carries a count
  // badge, so its accessible name is "Setup 1" and matching on the label alone
  // finds nothing.
  for (let i = 0; i < count; i++) {
    const button = buttons.nth(i);
    const name = (await button.innerText()).split("\n")[0].trim();
    await button.click();
    await page.waitForTimeout(400);
    await expect(page.locator("main"), `${name} rendered nothing`).not.toBeEmpty();
  }
  expect(errors).toEqual([]);
});

test("settings search finds a setting by its environment variable", async ({ page }) => {
  // The compose file is where people know these by name, so the variable has
  // to be searchable — and searching it must land on the section that holds it.
  const errors = watchForErrors(page);
  await page.goto("/settings", { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(900);

  await page.getByPlaceholder("Search settings\u2026").fill("SILT_KEEP_KEYS");
  await page.waitForTimeout(400);

  const rail = page.getByLabel("Settings sections");
  await expect(rail).toContainText("Keys kept readable");
  await rail.getByRole("button", { name: /Keys kept readable/ }).click();
  await page.waitForTimeout(400);

  await expect(page.locator("main")).toContainText("Collection");
  expect(errors).toEqual([]);
});

test("the live checks report on the endpoints they test", async ({ page }) => {
  // The suite runs against a deliberately unreachable Docker host, so the
  // probe has to say so rather than reporting healthy — which is the whole
  // reason it exists: an unreachable engine and an empty host look identical
  // everywhere else.
  const errors = watchForErrors(page);
  await page.goto("/settings", { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(900);

  await page.getByRole("button", { name: "Run checks" }).click();
  await expect(page.locator("main")).toContainText("Docker endpoint", { timeout: 10_000 });
  await expect(page.locator("main")).toContainText("Database");
  expect(errors).toEqual([]);
});

test("the status menu opens, reports the connection, and closes", async ({ page }) => {
  await page.goto("/", { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(1200);

  await page.click('button[aria-label="Status, version and preferences"]');
  const menu = page.locator('[role="menu"]');
  await expect(menu).toBeVisible();
  // Silt heartbeats every 20s, so an idle page must still be able to say it is
  // connected. This line is what makes a wedged stream distinguishable from a
  // quiet one.
  await expect(menu).toContainText("last heard from Silt");
  await expect(menu).toContainText("Receiving live updates");

  await page.keyboard.press("Escape");
  await expect(menu).toBeHidden();
});

test("a change arrives without reloading the page", async ({ page }) => {
  await page.goto("/", { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(1200);

  let navigations = 0;
  page.on("framenavigated", () => navigations++);

  const probe = `e2e probe ${Date.now()}`;
  const status = await page.evaluate(async (message) => {
    const res = await fetch("/api/ingest?token=demo", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ type: "e2e.probe", message, severity: "info" }),
    });
    return res.status;
  }, probe);
  expect(status).toBe(202);

  await expect(page.locator("body")).toContainText(probe, { timeout: 10_000 });
  expect(navigations, "the page reloaded instead of updating live").toBe(0);
});

test("a POSIX browser locale does not blank the page", async ({ page }) => {
  // A browser started under LANG=C, LANG=POSIX or LANG=en_US@posix reports
  // navigator.language as "en-US@posix", which is not a valid BCP 47 tag.
  // uPlot builds an Intl.NumberFormat from it at module scope, so importing
  // the chart threw, so the bundle threw, so the page was blank — on a locale
  // setting with nothing to do with charts.
  // Non-configurable on purpose: a page cannot count on being able to patch
  // the navigator back, which is why the fix guards the Intl constructors
  // instead of the value they read.
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "language", {
      configurable: false,
      get: () => "en-US@posix",
    });
  });

  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));

  // Not networkidle: the page holds the event stream open, so it never idles.
  await page.goto("/", { waitUntil: "domcontentloaded" });
  await expect(page.locator("header")).toBeVisible();
  // The chart is the thing that threw, so its presence is the assertion.
  await expect(page.locator(".uplot").first()).toBeVisible();
  expect(errors, "the page threw on a POSIX locale").toEqual([]);
});

test("a link to a different file changes the file on screen", async ({ page }) => {
  // The Files screen seeds its selection from the address once and then owns
  // it, which is right for the file picker and was wrong for a link: arriving
  // from a search hit at a different file while the screen was already open
  // changed the address and nothing else.
  await page.goto("/projects/1/files", { waitUntil: "domcontentloaded" });

  const picker = page.getByLabel("File", { exact: true });
  await expect(picker).toBeVisible({ timeout: 10_000 });

  const paths = await picker.locator("option").evaluateAll((options) =>
    options.map((o) => (o as HTMLOptionElement).value),
  );
  test.skip(paths.length < 2, "this project captured only one file");

  const current = await picker.inputValue();
  const other = paths.find((p) => p !== current) ?? paths[1];
  await page.goto(`/projects/1/files?path=${encodeURIComponent(other)}`, {
    waitUntil: "domcontentloaded",
  });
  await expect(page.getByLabel("File", { exact: true })).toHaveValue(other, { timeout: 10_000 });
});
