/**
 * A minimal history-API router.
 *
 * SvelteKit is not in play here and the app has five screens, so a routing
 * dependency would outweigh what it does. The Go server already falls back to
 * index.html for unknown paths, which is what makes deep links work.
 */

export type Route =
  | { name: "timeline" }
  | { name: "project"; projectId: number }
  | { name: "service"; projectId: number; service: string }
  | { name: "diff"; from?: number; to?: number; projectId?: number }
  | { name: "files"; projectId: number; path?: string }
  | { name: "settings" }
  | { name: "notfound"; path: string };

function parse(pathname: string, search: string): Route {
  const params = new URLSearchParams(search);
  const parts = pathname.replace(/^\/+|\/+$/g, "").split("/").filter(Boolean);

  if (parts.length === 0) return { name: "timeline" };

  if (parts[0] === "settings") return { name: "settings" };

  if (parts[0] === "diff") {
    const from = Number(params.get("from"));
    const to = Number(params.get("to"));
    const projectId = Number(params.get("project"));
    return {
      name: "diff",
      from: Number.isFinite(from) && from > 0 ? from : undefined,
      to: Number.isFinite(to) && to > 0 ? to : undefined,
      projectId: Number.isFinite(projectId) && projectId > 0 ? projectId : undefined,
    };
  }

  if (parts[0] === "projects" && parts[1]) {
    const projectId = Number(parts[1]);
    if (!Number.isFinite(projectId) || projectId <= 0) {
      return { name: "notfound", path: pathname };
    }
    if (parts[2] === "services" && parts[3]) {
      return { name: "service", projectId, service: decodeURIComponent(parts[3]) };
    }
    if (parts[2] === "files") {
      return { name: "files", projectId, path: params.get("path") ?? undefined };
    }
    return { name: "project", projectId };
  }

  return { name: "notfound", path: pathname };
}

function createRouter() {
  let route = $state<Route>(parse(location.pathname, location.search));

  if (typeof window !== "undefined") {
    window.addEventListener("popstate", () => {
      route = parse(location.pathname, location.search);
    });
  }

  return {
    get current() {
      return route;
    },
    navigate(url: string, replace = false) {
      const target = new URL(url, location.origin);
      if (replace) history.replaceState({}, "", target);
      else history.pushState({}, "", target);
      route = parse(target.pathname, target.search);
      window.scrollTo(0, 0);
    },
  };
}

export const router = createRouter();

/** Intercept in-app link clicks so anchors work without a full page load. */
export function link(node: HTMLAnchorElement) {
  function onClick(event: MouseEvent) {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
    const href = node.getAttribute("href");
    if (!href || href.startsWith("http") || href.startsWith("#")) return;
    event.preventDefault();
    router.navigate(href);
  }
  node.addEventListener("click", onClick);
  return { destroy: () => node.removeEventListener("click", onClick) };
}
