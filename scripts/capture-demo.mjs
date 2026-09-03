#!/usr/bin/env node
/**
 * Capture a running Silt into the fixture file the static demo reads.
 *
 * Usage: node scripts/capture-demo.mjs [baseURL] [outFile]
 *
 * The output is a build artefact, not source: it is written into web/dist
 * next to the bundle and fetched at runtime, so a normal build neither
 * contains it nor knows it exists.
 *
 * The published demo has no server behind it, so every GET the UI can make
 * has to be answered from a file. This crawls a real Silt — the one `make
 * demo` populates — and records the responses under the same keys
 * web/src/lib/demo.ts computes at request time.
 *
 * It walks rather than enumerates: the project list decides which project
 * URLs exist, the snapshot list decides which snapshot and diff URLs exist.
 * A hand-written list of paths would go stale the moment the demo seed
 * changes, and the failure would be a blank screen in the published demo
 * rather than an error here.
 */

import { writeFileSync } from "node:fs";

const base = (process.argv[2] || "http://127.0.0.1:8410").replace(/\/+$/, "");
const out = process.argv[3] || "web/dist/demo-fixtures.json";

/** The ranges the two range pickers offer, in ms. */
const TIMELINE_RANGES = [
  3_600_000, // 1h   Timeline
  21_600_000, // 6h   Timeline
  86_400_000, // 24h  Timeline, Project
  604_800_000, // 7d   Timeline, Project
  2_592_000_000, // 30d  Timeline, Project
  7_776_000_000, // 90d  Project
];

/** The snapshot-list queries the screens build, verbatim. */
const SNAPSHOT_QUERIES = ["", "?limit=100", "?changed_only=true&limit=100", "?limit=200"];

/** Searches worth having answers for: the demo's own project and image names. */
const SEARCHES = ["media", "immich", "postgres", "TZ", "restart"];

const fixtures = {};
let failures = 0;
let errors = 0;

/**
 * Fetch one path and record it under its normalised key.
 *
 * Mirrors fixtureKey() in web/src/lib/demo.ts: same path, query parameters
 * sorted. The two have to agree or the demo answers 404 for data it holds.
 */
async function capture(path) {
  const url = new URL(path, base);
  const params = [...url.searchParams.entries()].sort(([a], [b]) => a.localeCompare(b));
  const query = params.map(([k, v]) => `${k}=${v}`).join("&");
  const key = url.pathname + (query ? `?${query}` : "");
  if (key in fixtures) return fixtures[key];

  let res;
  try {
    res = await fetch(url, { headers: { Accept: "application/json" } });
  } catch (err) {
    console.error(`  ! ${key}: ${err.message}`);
    failures++;
    return null;
  }
  if (!res.ok) {
    // Recorded rather than dropped. A demo with no compose roots mounted
    // answers 503 for a file preview, and that is a state a real Silt reaches
    // too — replaying it shows the screen's own message instead of the shim's
    // "no demo data", which would be a lie about why the panel is empty.
    let body = null;
    try {
      body = await res.json();
    } catch {
      // No JSON to keep; the status alone carries the meaning.
    }
    console.error(`  · ${key}: HTTP ${res.status} (recorded)`);
    errors++;
    fixtures[key] = { __status: res.status, __body: body };
    return null;
  }
  const body = await res.json();
  fixtures[key] = body;
  return body;
}

/** Text endpoints (compose YAML) are stored as a string, not parsed. */
async function captureText(path) {
  const url = new URL(path, base);
  const params = [...url.searchParams.entries()].sort(([a], [b]) => a.localeCompare(b));
  const key = url.pathname + (params.length ? `?${params.map(([k, v]) => `${k}=${v}`).join("&")}` : "");
  if (key in fixtures) return;
  const res = await fetch(url);
  if (!res.ok) {
    console.error(`  ! ${key}: HTTP ${res.status}`);
    failures++;
    return;
  }
  fixtures[key] = await res.text();
}

async function main() {
  const to = Date.now();

  console.log(`capturing ${base}`);

  // Everything the app asks for before it knows anything.
  await capture("/api/version");
  await capture("/api/auth");
  await capture("/api/hosts");
  await capture("/api/settings");
  await capture("/api/overview");
  await capture("/api/events?limit=100");
  await capture("/api/audit?limit=100");
  const projects = (await capture("/api/projects")) || [];

  // Timelines: unfiltered, then per project. The demo shifts these onto the
  // current clock, so what matters is that every range the picker offers has
  // a capture of the right shape.
  for (const ms of TIMELINE_RANGES) {
    await capture(`/api/timeline?from=${to - ms}&to=${to}`);
  }
  await capture("/api/timeline");

  for (const q of SEARCHES) {
    await capture(`/api/search?q=${encodeURIComponent(q)}`);
  }

  console.log(`  ${projects.length} projects`);

  for (const p of projects) {
    await capture(`/api/projects/${p.id}`);
    await capture(`/api/projects/${p.id}/redaction-rules`);

    const services = (await capture(`/api/projects/${p.id}/services`)) || [];
    for (const s of services) {
      await capture(`/api/projects/${p.id}/services/${encodeURIComponent(s)}`);
    }

    for (const ms of TIMELINE_RANGES) {
      await capture(`/api/timeline?from=${to - ms}&project=${p.id}&to=${to}`);
    }

    // The limits the screens actually ask for. A bare list is not enough:
    // /api/projects/1/snapshots and ?limit=100 are different keys, and the
    // project screen only ever asks for the second.
    for (const q of SNAPSHOT_QUERIES) {
      await capture(`/api/projects/${p.id}/snapshots${q}`);
    }
    const snapshots = (await capture(`/api/projects/${p.id}/snapshots?limit=200`)) || [];

    const paths = (await capture(`/api/projects/${p.id}/files`)) || [];
    for (const path of paths) {
      await capture(`/api/projects/${p.id}/files/preview?path=${encodeURIComponent(path)}`);
    }

    // Every path any snapshot captured, which is what the Files screen's
    // selectors can reach between them.
    const filePaths = new Set();
    for (const snap of snapshots) {
      await capture(`/api/snapshots/${snap.id}`);
      await capture(`/api/snapshots/${snap.id}/compose`);
      await captureText(`/api/snapshots/${snap.id}/compose?format=yaml`);
      const files = (await capture(`/api/snapshots/${snap.id}/files`)) || [];
      for (const f of files) {
        if (!f.path) continue;
        filePaths.add(f.path);
        await capture(`/api/snapshots/${snap.id}/file?path=${encodeURIComponent(f.path)}`);
      }
    }

    // The snapshot pairs someone actually selects: each step through the
    // history, and the newest against every older one. The list is
    // newest-first.
    const pairs = [];
    for (let i = 0; i + 1 < snapshots.length; i++) {
      pairs.push([snapshots[i + 1].id, snapshots[i].id]);
    }
    for (let i = 2; i < snapshots.length; i++) {
      pairs.push([snapshots[i].id, snapshots[0].id]);
    }

    for (const [from, until] of pairs) {
      await capture(`/api/diff?from=${from}&to=${until}`);
      // The per-file diff is the Files screen, not the Diff screen, and
      // /api/diff does not list files — deriving the paths from it captured
      // nothing at all, which is how the compare view shipped with no data.
      for (const path of filePaths) {
        const q = `from=${from}&path=${encodeURIComponent(path)}&to=${until}`;
        await capture(`/api/diff/file?${q}`);
        // "Show the whole file" is one click away from every file diff.
        await capture(`/api/diff/file?context=full&${q}`);
      }
    }
  }

  // Stamped so the demo can shift every timestamp onto the reader's clock.
  // Without it the published demo ages: a fresh visitor sees a stack of
  // changes from whenever this ran, which reads as an abandoned deployment
  // rather than a live one.
  fixtures.__captured_at = to;

  const keys = Object.keys(fixtures).length - 1;
  const body = JSON.stringify(fixtures);
  writeFileSync(out, body);
  const bytes = body.length;
  console.log(`wrote ${out}: ${keys} responses, ${(bytes / 1024).toFixed(0)} KiB`);
  if (errors) console.log(`${errors} recorded as error responses`);
  if (failures) {
    console.error(`${failures} request(s) could not be reached`);
    process.exit(1);
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
