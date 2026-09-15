<script lang="ts">
  /**
   * How this install decides who you are — and, since 1.2.0, how to change it.
   *
   * This panel was entirely read-only, on the argument that a UI able to edit
   * the boundary in front of it would be a way in rather than a setting. What
   * changed the answer is that only an administrator reaches this screen, and
   * an administrator already holds the stronger controls: the whole-database
   * backup, the password, every session. Withholding these bought nothing and
   * cost a container recreate to fix a typo'd issuer.
   *
   * What it does cost is a way to lock yourself out, so the warning below is
   * part of the feature rather than decoration: SILT_SETTINGS_RESET is the way
   * back, and it is no use to anyone who finds out about it afterwards.
   *
   * Saving anything here rebuilds the whole gate — the account is re-read, the
   * forward-auth rules are rebuilt, provider discovery runs again. Sessions
   * survive it, because they are rows in the database rather than state in the
   * process.
   */
  import Field from "./Field.svelte";
  import Row from "./Row.svelte";
  import Toggle from "$lib/components/Toggle.svelte";
  import IntervalField from "./IntervalField.svelte";
  import { input } from "./input";
  import { SESSION_TTLS, IDLE_TTLS, ADMIN_TTLS } from "./intervals";
  import type { Settings } from "$lib/api/client";
  import type { SettingsStore } from "./store.svelte";

  let { store, id }: { store: SettingsStore; id: Settings["identity"] } = $props();
  const draft = $derived(store.draft);
  const effective = $derived(store.settings?.effective);

  // Whether this configuration would still let anyone in. Not a refusal — an
  // install with no authentication is a legal thing to run behind your own
  // proxy, and Silt has always said so in its logs — but it is worth saying
  // before the save rather than after.
  const wayIn = $derived(
    draft.local_account || draft.trust_proxy_auth || draft.oidc_issuer.trim() !== "",
  );
</script>

<section>
  <h3 class="text-sm font-semibold">Authentication</h3>
  <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
    Editable here, and applied without a restart: saving rebuilds the account, the forward-auth
    rules and the provider client. Sessions survive it — they are rows in Silt's database, not
    signed cookies, so changing a lifetime re-dates them rather than ending them.
  </p>

  <p class="mt-3 max-w-2xl rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs leading-relaxed">
    <b>If a change here locks you out</b>, set <code class="font-mono">SILT_SETTINGS_RESET=true</code>
    in your compose file and recreate the container. Every setting saved from this screen is
    dropped and Silt runs exactly what its environment says. Unset it afterwards, or the next
    restart drops them again.
  </p>

  {#if !wayIn}
    <p class="mt-3 max-w-2xl rounded-md border border-red-500/40 bg-red-500/10 px-3 py-2 text-xs leading-relaxed text-red-600 dark:text-red-300">
      As drafted, nothing here authenticates anyone: the built-in account is off, forward auth is
      off and no provider is configured. Saving this leaves Silt readable by anyone who can reach
      the address — which is a reasonable thing to run behind your own proxy, and not otherwise.
    </p>
  {/if}

  <h4 class="mt-5 text-xs font-medium uppercase tracking-wide text-muted-foreground">In effect</h4>
  <dl class="divide-y divide-border">
    <Row
      label="Method"
      value={id.mode === "none" ? "none — anyone who can reach this address can read it" : id.mode}
      hint="The first of these that is configured wins: an identity provider, then your reverse proxy, then the built-in account."
    />
    <Row
      label="Roles"
      value={id.roles_enabled
        ? "on — administrators change Silt's configuration, everyone else reads"
        : "off — everyone admitted may change everything"}
      hint={id.roles_enabled
        ? "A provider sign-in records the role in the session, so removing someone from the administrator group is not instant — it lapses within the window below, or immediately if you end their session under Security. A forward-auth proxy asserts the groups on every request, so there it is always immediate."
        : "Name an administrator group below to split reading from administering."}
    />
    {#if id.password_hash_set}
      <Row
        label="Password"
        value="set by the environment"
        envVar="SILT_PASSWORD_HASH"
        hint="Not editable, and deliberately so: the variable exists to take the password out of this UI's hands for an install managed from a compose file. Unset it to manage the password here."
      />
    {/if}
  </dl>

  <h4 class="mt-6 text-xs font-medium uppercase tracking-wide text-muted-foreground">
    Built-in account
  </h4>
  <div class="divide-y divide-border">
    <Field
      {store}
      name="local_account"
      label="Built-in account"
      envVar="SILT_LOCAL_ACCOUNT"
      hint="The single administrator with a password, managed under Security. Turn it off for an install that authenticates only through a provider or a proxy."
    >
      <Toggle id="local_account" bind:checked={draft.local_account} label="Built-in account" />
    </Field>
  </div>

  <h4 class="mt-6 text-xs font-medium uppercase tracking-wide text-muted-foreground">
    Reverse proxy
  </h4>
  <div class="divide-y divide-border">
    <Field
      {store}
      name="trust_proxy_auth"
      label="Forward auth"
      envVar="SILT_TRUST_PROXY_AUTH"
      hint="Believe an identity your reverse proxy asserts in a header. Authelia, authentik and tinyauth all do this."
    >
      <Toggle id="trust_proxy_auth" bind:checked={draft.trust_proxy_auth} label="Forward auth" />
    </Field>

    <Field
      {store}
      name="trusted_proxies"
      label="Trusted proxies"
      envVar="SILT_TRUSTED_PROXIES"
      hint="Addresses or CIDR ranges whose identity header is believed. Set this whenever forward auth is on: anyone who can open a socket can set a header, so without it “authenticated” means “reached the port” — which on a shared Docker network is every other container on it."
    >
      <input
        id="trusted_proxies"
        bind:value={draft.trusted_proxies}
        placeholder="172.18.0.0/16, 10.0.0.5"
        class="{input} font-mono text-xs"
      />
      {#if draft.trust_proxy_auth && draft.trusted_proxies.trim() === ""}
        <p class="mt-1.5 text-[11px] text-amber-600 dark:text-amber-400">
          Forward auth is on with no trusted source, so anything that can reach this port can claim
          to be anyone.
        </p>
      {/if}
    </Field>

    <Field {store} name="auth_header" label="Identity header" envVar="SILT_AUTH_HEADER">
      <input id="auth_header" bind:value={draft.auth_header} placeholder="X-Remote-User" class="{input} font-mono text-xs" />
    </Field>

    <Field
      {store}
      name="auth_groups_header"
      label="Groups header"
      envVar="SILT_AUTH_GROUPS_HEADER"
      hint="Read only when an administrator group is named below — reading an attacker-settable header for no reason is a habit worth not having."
    >
      <input id="auth_groups_header" bind:value={draft.auth_groups_header} placeholder="X-Remote-Groups" class="{input} font-mono text-xs" />
    </Field>

    <Field
      {store}
      name="admin_groups"
      label="Administrator groups"
      envVar="SILT_ADMIN_GROUPS"
      hint="Groups in that header which mean administrator. Empty means every forward-auth identity is one."
    >
      <input id="admin_groups" bind:value={draft.admin_groups} placeholder="silt-admins" class={input} />
    </Field>
  </div>

  <h4 class="mt-6 text-xs font-medium uppercase tracking-wide text-muted-foreground">
    OpenID Connect
  </h4>
  <div class="divide-y divide-border">
    <Field
      {store}
      name="oidc_issuer"
      label="Issuer"
      envVar="SILT_OIDC_ISSUER"
      hint="Paste it exactly as your provider prints it, trailing slash included. Saving re-runs discovery; a provider that cannot be reached leaves that login disabled and says why, rather than taking the rest of authentication with it."
    >
      <input
        id="oidc_issuer"
        bind:value={draft.oidc_issuer}
        placeholder="https://auth.example.com/application/o/silt/"
        class="{input} font-mono text-xs"
      />
    </Field>

    <Field {store} name="oidc_client_id" label="Client ID" envVar="SILT_OIDC_CLIENT_ID">
      <input id="oidc_client_id" bind:value={draft.oidc_client_id} class="{input} font-mono text-xs" />
    </Field>

    <Field
      {store}
      name="oidc_client_secret"
      label="Client secret"
      envVar="SILT_OIDC_CLIENT_SECRET"
      hint={id.oidc_secret_set
        ? "Configured. Typing here replaces it; it is never read back. Encrypted at rest when SILT_SECRET_KEY is set."
        : "Never read back. Encrypted at rest when SILT_SECRET_KEY is set — without that it sits in the database in plaintext, and a backup is a copy of that file."}
    >
      <input
        id="oidc_client_secret"
        type="password"
        autocomplete="new-password"
        bind:value={store.oidcClientSecret}
        placeholder={id.oidc_secret_set ? "•••••••• — type to replace" : "not configured"}
        class="{input} font-mono text-xs"
      />
    </Field>

    <Field
      {store}
      name="oidc_redirect_url"
      label="Redirect URL"
      envVar="SILT_OIDC_REDIRECT_URL"
      hint="Only needed if it is not the base URL plus /api/auth/callback. Must match your provider's registration exactly."
    >
      <input
        id="oidc_redirect_url"
        bind:value={draft.oidc_redirect_url}
        placeholder={effective?.base_url ? `${effective.base_url.replace(/\/$/, "")}/api/auth/callback` : "derived from each request"}
        class="{input} font-mono text-xs"
      />
    </Field>

    <Field {store} name="oidc_scopes" label="Scopes" envVar="SILT_OIDC_SCOPES" hint="openid is always included, whether listed or not.">
      <input id="oidc_scopes" bind:value={draft.oidc_scopes} placeholder="openid, profile, email" class={input} />
    </Field>

    <Field
      {store}
      name="oidc_username_claim"
      label="Username claim"
      envVar="SILT_OIDC_USERNAME_CLAIM"
      hint="Providers disagree. preferred_username, email and sub are the usual candidates."
    >
      <input id="oidc_username_claim" bind:value={draft.oidc_username_claim} placeholder="preferred_username" class="{input} font-mono text-xs" />
    </Field>

    <Field {store} name="oidc_groups_claim" label="Groups claim" envVar="SILT_OIDC_GROUPS_CLAIM" hint="Some providers call it roles.">
      <input id="oidc_groups_claim" bind:value={draft.oidc_groups_claim} placeholder="groups" class="{input} font-mono text-xs" />
    </Field>

    <Field
      {store}
      name="oidc_allowed_groups"
      label="Allowed groups"
      envVar="SILT_OIDC_ALLOWED_GROUPS"
      hint="Restricts who may sign in. Leave both allowlists empty to admit anyone the provider authenticates."
    >
      <input id="oidc_allowed_groups" bind:value={draft.oidc_allowed_groups} placeholder="silt-users" class={input} />
    </Field>

    <Field
      {store}
      name="oidc_allowed_users"
      label="Allowed users"
      envVar="SILT_OIDC_ALLOWED_USERS"
      hint="The same, by username, email or subject."
    >
      <input id="oidc_allowed_users" bind:value={draft.oidc_allowed_users} placeholder="you@example.com" class={input} />
    </Field>

    <Field
      {store}
      name="oidc_admin_groups"
      label="Administrator groups"
      envVar="SILT_OIDC_ADMIN_GROUPS"
      hint="Groups that mean administrator. Empty means everyone admitted is one. A group rather than a roles table here, because your provider already manages groups and two sources of truth agree until they do not."
    >
      <input id="oidc_admin_groups" bind:value={draft.oidc_admin_groups} placeholder="silt-admins" class={input} />
    </Field>
  </div>

  <h4 class="mt-6 text-xs font-medium uppercase tracking-wide text-muted-foreground">
    Sessions and cookies
  </h4>
  <div class="divide-y divide-border">
    <IntervalField
      {store}
      name="session_ttl_ms"
      label="Session lifetime"
      envVar="SILT_SESSION_TTL"
      hint="How long a session lasts regardless of activity. Shortening it re-dates the sessions that already exist rather than ending them; end them under Security."
      options={SESSION_TTLS}
    />

    <IntervalField
      {store}
      name="session_idle_ttl_ms"
      label="Idle timeout"
      envVar="SILT_SESSION_IDLE_TTL"
      hint="Ends an unused session early. Disabled means only the lifetime above applies."
      options={IDLE_TTLS}
    />

    <IntervalField
      {store}
      name="oidc_admin_ttl_ms"
      label="Administrator rights expire after"
      envVar="SILT_OIDC_ADMIN_TTL"
      hint="Provider sign-ins only. The groups an identity provider reports are read once, at sign-in, and there is nothing to re-read them from afterwards — so without this, removing someone from the administrator group took effect up to a session's length later. The session keeps working, read-only, once it lapses."
      options={ADMIN_TTLS}
    />

    <Field
      {store}
      name="cookie_secure"
      label="Secure cookie"
      envVar="SILT_COOKIE_SECURE"
      hint="auto infers it from the request, which a proxy that terminates TLS without setting X-Forwarded-Proto gets wrong — and wrong in the direction that ships the session cookie in the clear. Set it to always if you know your install is HTTPS."
    >
      <select id="cookie_secure" bind:value={draft.cookie_secure} class={input}>
        <option value="auto">auto — infer it from the request</option>
        <option value="always">always — this install is HTTPS</option>
        <option value="never">never — plain HTTP on a trusted network</option>
      </select>
    </Field>
  </div>
</section>
