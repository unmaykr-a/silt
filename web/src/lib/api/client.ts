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
export type ServiceHistory = components["schemas"]["ServiceHistory"];
export type ServiceObservation = components["schemas"]["ServiceObservation"];
export type EnvKeyChange = components["schemas"]["EnvKeyChange"];
export type Settings = components["schemas"]["Settings"];
export type SettingsValues = components["schemas"]["SettingsValues"];
export type SettingsPatch = components["schemas"]["SettingsPatch"];
export type VersionInfo = components["schemas"]["VersionInfo"];
export type Release = components["schemas"]["Release"];
export type PruneResult = components["schemas"]["PruneResult"];
export type ProjectModel = components["schemas"]["ProjectModel"];
export type AuthState = components["schemas"]["AuthState"];
export type SessionCount = components["schemas"]["SessionCount"];
export type ComposeFile = components["schemas"]["ComposeFile"];
export type FileContent = components["schemas"]["FileContent"];
export type FileDiff = components["schemas"]["FileDiff"];
export type DiffLine = components["schemas"]["DiffLine"];
export type FilePreview = components["schemas"]["FilePreview"];
export type PreviewLine = components["schemas"]["PreviewLine"];
export type RedactionRule = components["schemas"]["RedactionRule"];

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

async function request<T>(path: string, init: RequestInit): Promise<T> {
  const res = await fetch(path, { ...init, headers: { Accept: "application/json", ...init.headers } });
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
  timeline: (opts: { from?: number; to?: number; project?: number; bucket?: string } = {}, signal?: AbortSignal) => {
    const q = new URLSearchParams();
    if (opts.from) q.set("from", String(opts.from));
    if (opts.to) q.set("to", String(opts.to));
    if (opts.project) q.set("project", String(opts.project));
    if (opts.bucket) q.set("bucket", opts.bucket);
    const suffix = q.toString() ? `?${q}` : "";
    return get<Timeline>(`/api/timeline${suffix}`, signal);
  },
  compose: (snapshotId: number, signal?: AbortSignal) =>
    get<ProjectModel>(`/api/snapshots/${snapshotId}/compose`, signal),
  composeYaml: async (snapshotId: number, signal?: AbortSignal): Promise<string> => {
    const res = await fetch(`/api/snapshots/${snapshotId}/compose?format=yaml`, { signal });
    if (!res.ok) throw new ApiError(res.status, res.statusText);
    return res.text();
  },
  services: (projectId: number, signal?: AbortSignal) =>
    get<string[]>(`/api/projects/${projectId}/services`, signal),
  serviceHistory: (projectId: number, service: string, signal?: AbortSignal) =>
    get<ServiceHistory>(`/api/projects/${projectId}/services/${encodeURIComponent(service)}`, signal),
  settings: (signal?: AbortSignal) => get<Settings>("/api/settings", signal),
  updateSettings: (patch: SettingsPatch) =>
    request<Settings>("/api/settings", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    }),
  resetSettings: () => request<Settings>("/api/settings", { method: "DELETE" }),
  version: (signal?: AbortSignal) => get<VersionInfo>("/api/version", signal),
  takeSnapshot: (projectId: number) =>
    request<{ status: string }>(`/api/projects/${projectId}/snapshot`, { method: "POST" }),
  prune: () => request<PruneResult>("/api/maintenance/prune", { method: "POST" }),
  authState: (signal?: AbortSignal) => get<AuthState>("/api/auth", signal),
  login: (password: string) =>
    request<{ authenticated: boolean }>("/api/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ password }),
    }),
  logout: () => request<{ authenticated: boolean }>("/api/logout", { method: "POST" }),
  sessions: (signal?: AbortSignal) => get<SessionCount>("/api/auth/sessions", signal),
  setupAccount: (password: string) =>
    request<{ authenticated: boolean }>("/api/auth/setup", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ password }),
    }),
  changePassword: (current: string, password: string) =>
    request<{ changed: boolean }>("/api/auth/password", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ current, password }),
    }),
  setAccountEnabled: (enabled: boolean) =>
    request<{ enabled: boolean }>("/api/auth/account", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled }),
    }),
  unlinkAccount: () => request<{ linked: boolean }>("/api/auth/link", { method: "DELETE" }),
  /** Link the built-in account to the provider identity you sign in with next. */
  linkAccount: (next = location.pathname + location.search) => {
    location.href = `/api/auth/link?next=${encodeURIComponent(next)}`;
  },
  revokeSessions: () => request<{ revoked: number }>("/api/auth/sessions", { method: "DELETE" }),
  /**
   * Start the OpenID Connect flow.
   *
   * A full navigation rather than a fetch: the provider answers with a
   * redirect to its own login page, which only means anything to the browser's
   * address bar.
   */
  oidcLogin: (next = location.pathname + location.search) => {
    location.href = `/api/auth/login?next=${encodeURIComponent(next)}`;
  },

  snapshotFiles: (snapshotId: number, signal?: AbortSignal) =>
    get<ComposeFile[]>(`/api/snapshots/${snapshotId}/files`, signal),
  snapshotFile: (snapshotId: number, path: string, signal?: AbortSignal) =>
    get<FileContent>(`/api/snapshots/${snapshotId}/file?path=${encodeURIComponent(path)}`, signal),
  fileDiff: (from: number, to: number, path: string, full = false, signal?: AbortSignal) =>
    get<FileDiff>(
      `/api/diff/file?from=${from}&to=${to}&path=${encodeURIComponent(path)}${full ? "&context=full" : ""}`,
      signal,
    ),
  projectFiles: (projectId: number, signal?: AbortSignal) =>
    get<string[]>(`/api/projects/${projectId}/files`, signal),
  filePreview: (projectId: number, path: string, signal?: AbortSignal) =>
    get<FilePreview>(`/api/projects/${projectId}/files/preview?path=${encodeURIComponent(path)}`, signal),
  redactionRules: (projectId: number, signal?: AbortSignal) =>
    get<RedactionRule[]>(`/api/projects/${projectId}/redaction-rules`, signal),
  addRedactionRule: (
    projectId: number,
    rule: { path?: string; action: "hide" | "reveal"; kind: "key" | "line"; key?: string; line_no?: number },
  ) =>
    request<RedactionRule>(`/api/projects/${projectId}/redaction-rules`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(rule),
    }),
  deleteRedactionRule: async (projectId: number, ruleId: number) => {
    const res = await fetch(`/api/projects/${projectId}/redaction-rules/${ruleId}`, { method: "DELETE" });
    if (!res.ok) throw new ApiError(res.status, res.statusText);
  },
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
