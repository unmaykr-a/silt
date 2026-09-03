/**
 * URL parsing, with no runes in it.
 *
 * Split from router.svelte.ts for the reason PROJECT.md Section 15 records:
 * the vitest config has no Svelte plugin, so a `.svelte.ts` module cannot be
 * imported by a test. Route parsing is the kind of thing that should have a
 * test — it decides what every deep link resolves to — and it did not have one.
 */

export type Route =
  | { name: "timeline" }
  | { name: "project"; projectId: number }
  | { name: "service"; projectId: number; service: string }
  | { name: "diff"; from?: number; to?: number; projectId?: number }
  | { name: "files"; projectId: number; path?: string }
  | { name: "projects" }
  | { name: "search"; query: string }
  | { name: "settings" }
  | { name: "notfound"; path: string };

/** A positive integer, or undefined. Ids are never zero or negative. */
function id(raw: string | null): number | undefined {
  const n = Number(raw);
  return Number.isFinite(n) && n > 0 ? n : undefined;
}

export function parseRoute(pathname: string, search: string): Route {
  const params = new URLSearchParams(search);
  const parts = pathname.replace(/^\/+|\/+$/g, "").split("/").filter(Boolean);

  if (parts.length === 0) return { name: "timeline" };

  if (parts[0] === "settings") return { name: "settings" };
  if (parts[0] === "search") return { name: "search", query: params.get("q") ?? "" };
  if (parts[0] === "projects" && parts.length === 1) return { name: "projects" };

  if (parts[0] === "diff") {
    return {
      name: "diff",
      from: id(params.get("from")),
      to: id(params.get("to")),
      projectId: id(params.get("project")),
    };
  }

  if (parts[0] === "projects" && parts[1]) {
    const projectId = id(parts[1]);
    if (projectId === undefined) return { name: "notfound", path: pathname };

    if (parts[2] === "services" && parts[3]) {
      return { name: "service", projectId, service: decodeURIComponent(parts[3]) };
    }
    if (parts[2] === "files" && parts.length === 3) {
      return { name: "files", projectId, path: params.get("path") ?? undefined };
    }
    if (parts.length === 2) return { name: "project", projectId };
  }

  return { name: "notfound", path: pathname };
}

/**
 * Remove the mount point, so parsing only ever sees app-relative paths.
 *
 * Silt serves itself from the root, so the base is empty in every real
 * install. The demo published to GitHub Pages lives under a project path, and
 * without this every route there would parse as "not found" — the base would
 * be read as the first path segment.
 *
 * A prefix match is not enough: `/siltation` is not under `/silt`.
 */
export function stripBase(pathname: string, base: string): string {
  if (!base) return pathname;
  if (pathname === base || pathname === base + "/") return "/";
  if (pathname.startsWith(base + "/")) return pathname.slice(base.length);
  return pathname;
}

/** Add the mount point, for anything that becomes a real URL. */
export function joinBase(path: string, base: string): string {
  if (!base) return path;
  if (/^[a-z]+:\/\//i.test(path)) return path;
  if (path === base || path.startsWith(base + "/")) return path;
  return base + (path.startsWith("/") ? path : "/" + path);
}
