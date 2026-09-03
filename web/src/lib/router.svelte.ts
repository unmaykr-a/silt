/**
 * A minimal history-API router.
 *
 * SvelteKit is not in play here and the app has five screens, so a routing
 * dependency would outweigh what it does. The Go server already falls back to
 * index.html for unknown paths, which is what makes deep links work.
 */

import { parseRoute, stripBase, joinBase, type Route } from "./routing";

export type { Route };

/**
 * Where the app is mounted.
 *
 * "/" in every real install, since Silt serves itself from the root. The demo
 * on GitHub Pages lives under a project path.
 */
export const BASE = (import.meta.env.BASE_URL || "/").replace(/\/+$/, "");

/** Prefix the mount point, for anything that becomes a real URL. */
export function href(path: string): string {
  return joinBase(path, BASE);
}

function createRouter() {
  let route = $state<Route>(parseRoute(stripBase(location.pathname, BASE), location.search));

  if (typeof window !== "undefined") {
    window.addEventListener("popstate", () => {
      route = parseRoute(stripBase(location.pathname, BASE), location.search);
    });
  }

  return {
    get current() {
      return route;
    },
    navigate(url: string, replace = false) {
      const target = new URL(href(url), location.origin);
      if (replace) history.replaceState({}, "", target);
      else history.pushState({}, "", target);
      route = parseRoute(stripBase(target.pathname, BASE), target.search);
      window.scrollTo(0, 0);
    },
  };
}

export const router = createRouter();

/**
 * Intercept in-app link clicks so anchors work without a full page load.
 *
 * Also rewrites the href onto the mount point. Every `href` in the app is
 * written app-relative ("/projects/3"), which is the same thing under a base
 * of "/". Under the demo's project path it is not: intercepting the click
 * would be enough for a plain click and nothing else — the status bar would
 * show the wrong URL, and ctrl-click, middle-click and "open in new tab"
 * would all leave the app.
 */
export function link(node: HTMLAnchorElement) {
  function appPath(): string | null {
    const raw = node.getAttribute("href");
    if (!raw || /^[a-z]+:/i.test(raw) || raw.startsWith("//") || raw.startsWith("#")) return null;
    return stripBase(raw, BASE);
  }

  function rewrite() {
    const path = appPath();
    if (path === null) return;
    const full = href(path);
    if (node.getAttribute("href") !== full) node.setAttribute("href", full);
  }

  function onClick(event: MouseEvent) {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
    const path = appPath();
    if (path === null) return;
    event.preventDefault();
    router.navigate(path);
  }

  rewrite();
  node.addEventListener("click", onClick);
  return {
    // Svelte re-runs this when the href changes, e.g. a project link in a list
    // that is re-sorted rather than re-created.
    update: rewrite,
    destroy: () => node.removeEventListener("click", onClick),
  };
}
