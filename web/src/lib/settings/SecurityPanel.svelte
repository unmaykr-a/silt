<script lang="ts">
  /**
   * What is protecting this screen, and the two things you can do about it:
   * manage the built-in account, and end every session.
   *
   * The reported half is read-only on purpose — every setting on it is the
   * boundary in front of this page, so a UI that could edit them would be a way
   * in rather than a setting. Revoking sessions is the exception that proves
   * it: it is the one action you want when you think a token has leaked, and it
   * cannot widen anything.
   */
  import Row from "./Row.svelte";
  import AccountPanel from "./AccountPanel.svelte";
  import AuditLog from "$lib/components/AuditLog.svelte";
  import { Button } from "$lib/components/ui/button";
  import { api, type AuthState, type Settings } from "$lib/api/client";
  import type { SettingsStore } from "./store.svelte";

  let { store, fixed }: { store: SettingsStore; fixed: Settings["fixed"] } = $props();

  let authState = $state<AuthState | null>(null);
  let sessionCount = $state<number | null>(null);
  let revoking = $state(false);

  const AUTH_MODE_LABEL: Record<string, string> = {
    none: "None — anyone who can reach this port has full read access",
    proxy: "Reverse proxy header",
    password: "Password",
    "proxy+password": "Reverse proxy header, with a password fallback",
  };

  $effect(() => {
    const controller = new AbortController();
    Promise.all([api.authState(controller.signal), api.sessions(controller.signal)])
      .then(([a, s]) => {
        authState = a;
        sessionCount = s.count;
      })
      .catch(() => {});
    return () => controller.abort();
  });

  async function refresh() {
    try {
      authState = await api.authState();
    } catch {
      // Leave the last good answer on screen; the action's own error already
      // said what went wrong.
    }
  }

  async function revokeAll() {
    revoking = true;
    try {
      await api.revokeSessions();
      // Every session went, including this one, so the app has to re-check.
      location.reload();
    } catch (err) {
      store.error = (err as Error).message;
      revoking = false;
    }
  }
</script>

<section>
  <h3 class="text-sm font-semibold">Security</h3>
  <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
    Nothing here is editable, and that is the point: every one of these is the boundary protecting
    this screen. A UI that could turn off the login in front of it would be a way in rather than a
    setting. Change them in your environment and recreate the container.
  </p>

  <dl class="mt-3 divide-y divide-border">
    <Row
      label="Sign-in method"
      value={AUTH_MODE_LABEL[fixed.auth_mode] ?? fixed.auth_mode}
      envVar="SILT_OIDC_ISSUER / SILT_TRUST_PROXY_AUTH / SILT_PASSWORD_HASH"
      hint={fixed.auth_mode === "none"
        ? "Silt is open. That is the right default for something behind your own proxy, and the wrong one for anything else."
        : undefined}
    />

    {#if authState?.oidc_enabled}
      <Row label="Identity provider" value={authState.oidc_issuer ?? "configured"} envVar="SILT_OIDC_ISSUER" />
    {:else if authState?.oidc_error}
      <Row
        label="Identity provider"
        value="configured, but unreachable"
        envVar="SILT_OIDC_ISSUER"
        hint={authState.oidc_error}
      />
    {/if}

    <Row
      label="Signed-in sessions"
      value={sessionCount === null ? "…" : String(sessionCount)}
      envVar="SILT_SESSION_TTL / SILT_SESSION_IDLE_TTL"
      hint="Sessions are rows in Silt's database, not signed cookies. Signing out revokes one; the button below revokes all of them."
    />
  </dl>

  {#if authState?.local_available}
    <AccountPanel
      {authState}
      onchanged={refresh}
      onerror={(m) => {
        store.error = m;
        store.notice = null;
      }}
      onnotice={(m) => {
        store.notice = m;
        store.error = null;
      }}
    />
  {/if}

  <h4 class="mt-8 text-sm font-medium">Sessions</h4>
  <div class="mt-3">
    <Button variant="outline" size="sm" onclick={revokeAll} disabled={revoking || !authState?.required}>
      {revoking ? "Signing out…" : "Sign out everywhere"}
    </Button>
    <p class="mt-1.5 max-w-xl text-xs text-muted-foreground/70">
      {#if authState?.required}
        Ends every session, including this one. Use it if you think a session token has leaked — a
        cookie the browser throws away is still a working credential to anyone who copied it, but a
        deleted row is not.
      {:else}
        There is nothing to revoke while no authentication is configured.
      {/if}
    </p>
  </div>

  <div class="mt-8">
    <AuditLog />
  </div>
</section>
