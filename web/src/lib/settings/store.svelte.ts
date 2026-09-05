/**
 * The settings screen's shared state.
 *
 * Ten panels read and write the same draft, the same error and notice lines,
 * and the same "is anything unsaved" answer, so it lives in one place and is
 * handed to each panel as a prop. Explicitly, rather than through context: a
 * panel's dependencies should be visible where it is used.
 *
 * A factory rather than module-level state, because module-level state would
 * outlive the screen. Navigating away and back would show the previous visit's
 * unsaved draft and stale error, which is the kind of bug nobody reports and
 * everybody works around.
 *
 * The logic that can be tested is not here — see patch.ts, which this wraps.
 */

import { api, type Settings, type PruneResult, type AuthState } from "$lib/api/client";
import { buildPatch, emptyDraft, toDraft, type Draft } from "./patch";

export function createSettingsStore() {
  let settings = $state<Settings | null>(null);
  let draft = $state<Draft>(emptyDraft());
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let saving = $state(false);

  // Write-only. Never returned by the API, so they start empty every time and
  // travel only when someone types into them.
  let notifyUrls = $state("");
  let ingestToken = $state("");

  // What this identity may do. A viewer reads every screen and changes
  // nothing, so the controls that would be refused are not offered at all: a
  // save button that always fails is worse than no save button.
  let role = $state<string>("admin");

  function adopt(s: Settings) {
    settings = s;
    draft = toDraft(s.effective);
    notifyUrls = "";
    ingestToken = "";
  }

  async function apply(fn: () => Promise<Settings>, message?: string) {
    saving = true;
    try {
      adopt(await fn());
      error = null;
      notice = message ?? null;
    } catch (err) {
      error = (err as Error).message;
      notice = null;
    } finally {
      saving = false;
    }
  }

  return {
    get settings() {
      return settings;
    },
    get draft() {
      return draft;
    },
    get error() {
      return error;
    },
    set error(v: string | null) {
      error = v;
    },
    get notice() {
      return notice;
    },
    set notice(v: string | null) {
      notice = v;
    },
    get saving() {
      return saving;
    },
    get notifyUrls() {
      return notifyUrls;
    },
    set notifyUrls(v: string) {
      notifyUrls = v;
    },
    get ingestToken() {
      return ingestToken;
    },
    set ingestToken(v: string) {
      ingestToken = v;
    },
    get role() {
      return role;
    },

    /** Which settings are set here rather than by the environment. */
    get overridden() {
      return new Set(settings?.overridden ?? []);
    },
    get readOnly() {
      return role === "viewer";
    },
    /** Whether a save would send anything. */
    get dirty() {
      if (!settings) return false;
      return Object.keys(buildPatch(draft, settings.effective, { notifyUrls, ingestToken })).length > 0;
    },

    adopt,
    apply,

    async load(signal?: AbortSignal) {
      try {
        adopt(await api.settings(signal));
        error = null;
      } catch (err) {
        if ((err as Error).name !== "AbortError") error = (err as Error).message;
      }
    },

    async loadRole(signal?: AbortSignal) {
      try {
        const a: AuthState = await api.authState(signal);
        role = a.role ?? "admin";
      } catch {
        // An install with no authentication has no role to report, and the
        // default is the permissive one it already had.
      }
    },

    save() {
      if (!settings) return;
      const patch = buildPatch(draft, settings.effective, { notifyUrls, ingestToken });
      if (Object.keys(patch).length === 0) return;
      return apply(() => api.updateSettings(patch), "Saved. In force now — no restart needed.");
    },

    useEnvironment: (field: string) => apply(() => api.updateSettings({ reset: [field] })),

    resetAll: () =>
      apply(
        () => api.resetSettings(),
        "Every override dropped. Silt is running what its environment says.",
      ),

    revert() {
      if (settings) adopt(settings);
      notice = null;
    },

    async prune(): Promise<PruneResult | null> {
      try {
        const out = await api.prune();
        settings = await api.settings();
        error = null;
        return out;
      } catch (err) {
        error = (err as Error).message;
        return null;
      }
    },
  };
}

export type SettingsStore = ReturnType<typeof createSettingsStore>;
