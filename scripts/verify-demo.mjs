#!/usr/bin/env node
/**
 * Walk the built demo in a browser and fail on anything the capture missed.
 *
 * Usage: node scripts/verify-demo.mjs [siteDir] [basePath]
 *
 * The demo's failure mode is quiet: a URL the capture never reached answers
 * 404, and the screen shows its own empty state. Published, that is a demo
 * with a blank page in it and nobody to notice. So the site is served the way
 * GitHub Pages serves it — 404.html for unknown paths, which is what makes
 * client-side routes survive a reload — and every screen is visited and
 * checked for the shim's own "No demo data for" message.
 *
 * It drives the real routes rather than replaying the capture's URL list,
 * because the two agreeing proves nothing: the question is whether the
 * screens' requests are covered, not whether the capture matches itself.
 */

import { createServer } from "node:http";
import { readFile, stat } from "node:fs/promises";
import { extname, join, resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";

const siteDir = resolve(process.argv[2] || ".demo-site");
const basePath = (process.argv[3] || "/silt/").replace(/\/+$/, "");
const PORT = Number(process.env.SILT_DEMO_VERIFY_PORT || 8414);

// Playwright lives in e2e/, which is the only place in the repository that has
// it. Resolved from this file rather than the working directory so the script
// runs the same from anywhere.
const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const require = createRequire(join(repoRoot, "e2e", "package.json"));
const { chromium } = require("playwright-core");

const TYPES = {
  ".html": "text/html",
  ".js": "text/javascript",
  ".css": "text/css",
  ".json": "application/json",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".ico": "image/x-icon",
};

/** A static server that behaves like GitHub Pages, 404.html included. */
function serve() {
  return new Promise((ready) => {
    const server = createServer(async (req, res) => {
      const path = decodeURIComponent(req.url.split("?")[0]);
      const under = path === basePath || path.startsWith(basePath + "/");
      let file = under ? join(siteDir, path.slice(basePath.length)) : join(siteDir, path);
      try {
        if ((await stat(file)).isDirectory()) file = join(file, "index.html");
        const body = await readFile(file);
        res.writeHead(200, { "Content-Type": TYPES[extname(file)] || "application/octet-stream" });
        res.end(body);
      } catch {
        try {
          res.writeHead(404, { "Content-Type": "text/html" });
          res.end(await readFile(join(siteDir, "404.html")));
        } catch {
          res.writeHead(404);
          res.end("not found");
        }
      }
    });
    server.listen(PORT, () => ready(server));
  });
}

async function main() {
  const server = await serve();
  const origin = `http://127.0.0.1:${PORT}${basePath}`;

  const browser = await chromium.launch({
    executablePath: process.env.PLAYWRIGHT_CHROMIUM || undefined,
  });
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });

  const problems = [];
  page.on("pageerror", (err) => problems.push(`page error: ${err.message}`));

  // The fixtures answer relative to the page, so the demo's own misses are the
  // signal. Anything else 404ing is the Pages fallback and expected.
  page.on("response", (res) => {
    if (res.url().includes("demo-fixtures.json") && !res.ok()) {
      problems.push(`fixtures did not load: HTTP ${res.status()}`);
    }
  });

  async function visit(label, path) {
    await page.goto(origin + path, { waitUntil: "networkidle" });
    await page.waitForTimeout(400);
    const text = await page.locator("body").innerText();
    for (const line of text.split("\n")) {
      if (line.includes("No demo data for")) problems.push(`${label}: ${line.trim()}`);
    }
    return text;
  }

  await visit("timeline", "/");
  await visit("projects", "/projects");
  await visit("settings", "/settings");
  await visit("search", "/search?q=media");
  await visit("not found", "/nope");

  // Fixtures are the source of truth for what exists — read the same file the
  // page reads rather than assuming ids.
  const fixtures = JSON.parse(await readFile(join(siteDir, "demo-fixtures.json"), "utf8"));
  const projects = fixtures["/api/projects"] || [];

  for (const p of projects) {
    await visit(`project ${p.name}`, `/projects/${p.id}`);
    await visit(`files ${p.name}`, `/projects/${p.id}/files`);
    await visit(`diff picker ${p.name}`, `/diff?project=${p.id}`);

    // A diff with two snapshots actually selected. Without this the check
    // only ever saw "pick two snapshots to compare", which is why the
    // per-file diffs could be absent from the capture and still pass.
    const snaps = fixtures[`/api/projects/${p.id}/snapshots?limit=200`] || [];
    if (snaps.length >= 2) {
      const [to, from] = snaps;
      await visit(`diff ${p.name}`, `/diff?from=${from.id}&to=${to.id}&project=${p.id}`);
    }

    const services = fixtures[`/api/projects/${p.id}/services`] || [];
    for (const s of services.slice(0, 2)) {
      await visit(`service ${p.name}/${s}`, `/projects/${p.id}/services/${encodeURIComponent(s)}`);
    }
  }

  await browser.close();
  server.close();

  if (problems.length) {
    console.error(`demo verification failed: ${problems.length} problem(s)`);
    for (const p of [...new Set(problems)]) console.error(`  ${p}`);
    process.exit(1);
  }
  console.log(`demo verified: ${projects.length} projects, no missing fixtures`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
