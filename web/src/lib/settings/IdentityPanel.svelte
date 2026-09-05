<script lang="ts">
  /**
   * How this install decides who you are. Entirely read-only: every setting
   * here is the boundary protecting this screen, so a UI that could edit them
   * would be a way in rather than a setting.
   */
  import Row from "./Row.svelte";
  import FlagRow from "./FlagRow.svelte";
  import { duration } from "$lib/format";
  import type { Settings } from "$lib/api/client";

  let { id }: { id: Settings["identity"] } = $props();
</script>

<section>
  <h3 class="text-sm font-semibold">Authentication</h3>
  <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
    How this install decides who you are. Read-only, and for the sharper of the two reasons the
    environment-only settings are: these are the boundary protecting this screen, so a UI that could
    edit them would be a way in rather than a setting. Shown at all because twelve variables were
    readable nowhere — and when forward auth is not working, the first question is what Silt thinks
    it was told.
  </p>

  <h4 class="mt-5 text-xs font-medium uppercase tracking-wide text-muted-foreground">In effect</h4>
  <dl class="divide-y divide-border">
    <Row
      label="Roles"
      value={id.roles_enabled
        ? "on — administrators change Silt's configuration, everyone else reads"
        : "off — everyone admitted may change everything"}
      envVar="SILT_OIDC_ADMIN_GROUPS / SILT_ADMIN_GROUPS"
      hint={id.roles_enabled
        ? "A provider sign-in records the role in the session, so removing someone from the administrator group is not instant — it lapses within the window below, or immediately if you end their session under Security. A forward-auth proxy asserts the groups on every request, so there it is always immediate."
        : undefined}
    />
    <Row
      label="Method"
      value={id.mode === "none" ? "none — anyone who can reach this address can read it" : id.mode}
      hint="The first of these that is configured wins: an identity provider, then your reverse proxy, then the built-in account."
    />
    <FlagRow
      label="Built-in account"
      on={id.local_account}
      envVar="SILT_LOCAL_ACCOUNT"
      hint="Silt's own administrator. Off leaves only the provider."
    />
    <FlagRow
      label="Password claimed at startup"
      on={id.password_hash_set}
      envVar="SILT_PASSWORD_HASH"
      hint="Set, the account is claimed before Silt starts and the first-run window never exists."
    />
    <Row label="Session lifetime" value={duration(id.session_ttl_ms)} envVar="SILT_SESSION_TTL" />
    <Row label="Idle timeout" value={duration(id.session_idle_ttl_ms)} envVar="SILT_SESSION_IDLE_TTL" />
    <Row
      label="Administrator rights expire after"
      value={id.oidc_admin_ttl_ms > 0 ? duration(id.oidc_admin_ttl_ms) : "never"}
      envVar="SILT_OIDC_ADMIN_TTL"
      hint={id.oidc_admin_ttl_ms > 0
        ? "A provider's groups are read once, at sign-in. After this the session keeps working, read-only, until they sign in again — which is what bounds how long a removed administrator stays one."
        : "A provider's groups are read once, at sign-in, and with no window here a removed administrator stays one for the whole session lifetime."}
    />
    <Row
      label="Secure cookie"
      value={id.cookie_secure === "auto" ? "auto — inferred from the request" : id.cookie_secure}
      envVar="SILT_COOKIE_SECURE"
      hint={id.cookie_secure === "auto"
        ? "A proxy that terminates TLS without setting X-Forwarded-Proto makes this look like plain HTTP, and the session cookie ships without Secure. Set it to always if you know your install is HTTPS."
        : undefined}
    />
    <FlagRow
      label="Metrics without signing in"
      on={id.metrics_public}
      envVar="SILT_METRICS_PUBLIC"
      hint="/metrics carries counts and names, not values — but a project name is still information about your host."
    />
  </dl>

  <h4 class="mt-6 text-xs font-medium uppercase tracking-wide text-muted-foreground">Reverse proxy</h4>
  <dl class="divide-y divide-border">
    <FlagRow label="Trust an asserted identity" on={id.trust_proxy_auth} envVar="SILT_TRUST_PROXY_AUTH" />
    <Row label="Identity header" value={id.auth_header || "not set"} envVar="SILT_AUTH_HEADER" />
    <Row
      label="Groups header"
      value={id.auth_groups_header || "not set"}
      envVar="SILT_AUTH_GROUPS_HEADER"
      hint="Read only when administrator groups are configured: without a rule there is nothing to compare against, and reading an attacker-settable header for no reason is a habit worth not having."
    />
    <Row
      label="Administrator groups"
      value={id.admin_groups.join(", ") || "everyone admitted is an administrator"}
      envVar="SILT_ADMIN_GROUPS"
    />
    <Row
      label="Trusted proxies"
      value={id.trusted_proxies.length ? id.trusted_proxies.join(", ") : "not set"}
      envVar="SILT_TRUSTED_PROXIES"
      hint="The whole security of forward auth. The header is settable by anything that can open a socket, so with no list here &ldquo;authenticated&rdquo; means &ldquo;reached the port&rdquo;."
    />
  </dl>

  <h4 class="mt-6 text-xs font-medium uppercase tracking-wide text-muted-foreground">OpenID Connect</h4>
  <dl class="divide-y divide-border">
    <Row label="Issuer" value={id.oidc_issuer || "not set"} envVar="SILT_OIDC_ISSUER" />
    <Row label="Client ID" value={id.oidc_client_id || "not set"} envVar="SILT_OIDC_CLIENT_ID" />
    <FlagRow
      label="Client secret"
      on={id.oidc_secret_set}
      envVar="SILT_OIDC_CLIENT_SECRET"
      hint="Set or not, never shown — like the notification targets and the ingest token."
    />
    <Row
      label="Redirect URL"
      value={id.oidc_redirect_url || "defaults to the base URL + /api/auth/callback"}
      envVar="SILT_OIDC_REDIRECT_URL"
    />
    <Row label="Scopes" value={id.oidc_scopes.join(", ") || "not set"} envVar="SILT_OIDC_SCOPES" />
    <Row
      label="Username claim"
      value={id.oidc_username_claim || "not set"}
      envVar="SILT_OIDC_USERNAME_CLAIM"
      hint="Providers disagree about these two, which is the usual reason a sign-in works but names nobody."
    />
    <Row label="Groups claim" value={id.oidc_groups_claim || "not set"} envVar="SILT_OIDC_GROUPS_CLAIM" />
    <Row
      label="Allowed groups"
      value={id.oidc_allowed_groups.join(", ") || "any"}
      envVar="SILT_OIDC_ALLOWED_GROUPS"
      hint="Both allowlists empty admits anyone the provider will authenticate."
    />
    <Row label="Allowed users" value={id.oidc_allowed_users.join(", ") || "any"} envVar="SILT_OIDC_ALLOWED_USERS" />
    <Row
      label="Administrator groups"
      value={id.oidc_admin_groups.join(", ") || "everyone admitted is an administrator"}
      envVar="SILT_OIDC_ADMIN_GROUPS"
      hint="Membership makes an identity an administrator. Empty is what Silt did before roles existed — turning an upgrade into a lockout for the person who configured it would be the worst possible default."
    />
  </dl>
</section>
