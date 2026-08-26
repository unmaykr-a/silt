// A thin typed client over the generated OpenAPI schema.
//
// The types come from api/openapi.yaml via `npm run api-types`, which the
// build runs before typechecking — so a spec change that breaks the frontend
// fails the build instead of surfacing as an undefined at runtime.
import type { components } from "./schema";

export type Host = components["schemas"]["Host"];
export type Project = components["schemas"]["Project"];
export type Snapshot = components["schemas"]["Snapshot"];
export type SnapshotDetail = components["schemas"]["SnapshotDetail"];
export type Event = components["schemas"]["Event"];
export type Diff = components["schemas"]["Diff"];
export type Change = components["schemas"]["Change"];
export type Timeline = components["schemas"]["Timeline"];

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(path, { signal, headers: { Accept: "application/json" } });
  if (!res.ok) {
    let message = res.statusText;
    try {
      const body = (await res.json()) as { error?: string };
      if (body.error) message = body.error;
    } catch {
      // Non-JSON error body; the status text will have to do.
    }
    throw new ApiError(res.status, message);
  }
  return (await res.json()) as T;
}

export const api = {
  hosts: (signal?: AbortSignal) => get<Host[]>("/api/hosts", signal),
  projects: (signal?: AbortSignal) => get<Project[]>("/api/projects", signal),
  project: (id: number, signal?: AbortSignal) => get<Project>(`/api/projects/${id}`, signal),
  snapshots: (projectId: number, opts: { changedOnly?: boolean; limit?: number } = {}, signal?: AbortSignal) => {
    const q = new URLSearchParams();
    if (opts.changedOnly) q.set("changed_only", "true");
    if (opts.limit) q.set("limit", String(opts.limit));
    const suffix = q.toString() ? `?${q}` : "";
    return get<Snapshot[]>(`/api/projects/${projectId}/snapshots${suffix}`, signal);
  },
  snapshot: (id: number, signal?: AbortSignal) => get<SnapshotDetail>(`/api/snapshots/${id}`, signal),
  diff: (from: number, to: number, signal?: AbortSignal) =>
    get<Diff>(`/api/diff?from=${from}&to=${to}`, signal),
  events: (limit = 100, signal?: AbortSignal) => get<Event[]>(`/api/events?limit=${limit}`, signal),
  timeline: (signal?: AbortSignal) => get<Timeline>("/api/timeline", signal),
};

/** Named SSE events the server emits on /api/stream. */
export type StreamEvent = "ready" | "event" | "snapshot.changed";

/**
 * Subscribe to the live stream. Returns an unsubscribe function.
 *
 * EventSource reconnects on its own, so there is no retry logic here; the
 * server's `ready` event marks each successful (re)connection.
 */
export function subscribe(
  handlers: Partial<Record<StreamEvent, (data: unknown) => void>>,
): () => void {
  const source = new EventSource("/api/stream");
  const listeners: Array<[string, EventListener]> = [];

  for (const [name, handler] of Object.entries(handlers)) {
    if (!handler) continue;
    const listener: EventListener = (ev) => {
      try {
        handler(JSON.parse((ev as MessageEvent).data));
      } catch {
        // A malformed frame should not take down the subscription.
      }
    };
    source.addEventListener(name, listener);
    listeners.push([name, listener]);
  }

  return () => {
    for (const [name, listener] of listeners) source.removeEventListener(name, listener);
    source.close();
  };
}
