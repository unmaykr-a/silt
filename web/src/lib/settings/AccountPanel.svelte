<script lang="ts">
  /**
   * The built-in account: its password, its link to a provider identity, and
   * whether it is on at all.
   *
   * Its own file because it is not a settings section. Everything around it
   * under Security reports what is in force and cannot change it — that is the
   * point of that section — while this is three write paths and their forms,
   * and it made Security the largest thing on the screen by a factor of two.
   */
  import { Button } from "$lib/components/ui/button";
  import { api, type AuthState } from "$lib/api/client";
  import { input } from "./input";

  let {
    authState,
    onchanged,
    onerror,
    onnotice,
  }: {
    authState: AuthState;
    /** Re-read the auth state after anything here changes it. */
    onchanged: () => Promise<void>;
    onerror: (message: string) => void;
    onnotice: (message: string) => void;
  } = $props();

  let currentPassword = $state("");
  let newPassword = $state("");
  let confirmPassword = $state("");
  let changing = $state(false);
  let toggling = $state(false);

  const minimum = $derived(authState.min_password_length ?? 10);
  const matches = $derived(newPassword === confirmPassword);
  const longEnough = $derived(newPassword.length >= minimum);
  const canSetFirst = $derived(longEnough && matches);
  const canChange = $derived(currentPassword !== "" && longEnough && matches);
  const mismatch = $derived(confirmPassword !== "" && !matches);

  function clear() {
    currentPassword = "";
    newPassword = "";
    confirmPassword = "";
  }

  /** Every action here has the same shape: run it, report, re-read.
   *
   *  Promise<unknown> rather than Promise<void>: the API calls return their
   *  new state and nothing here reads it, but a signature that refused a
   *  return value would make each one need a wrapper to throw it away. */
  async function run(fn: () => Promise<unknown>, message: string) {
    changing = true;
    try {
      await fn();
      clear();
      onnotice(message);
      await onchanged();
    } catch (err) {
      onerror((err as Error).message);
    } finally {
      changing = false;
    }
  }

  const setFirstPassword = () =>
    run(
      () => api.setupAccount(newPassword),
      "Password set. You can now sign in without your provider.",
    );

  const changePassword = () =>
    run(
      () => api.changePassword(currentPassword, newPassword),
      "Password changed. Every other signed-in browser was signed out.",
    );

  const unlink = () =>
    run(
      () => api.unlinkAccount(),
      "Unlinked. That provider identity no longer reaches this account.",
    );

  async function setEnabled(enabled: boolean) {
    toggling = true;
    try {
      await api.setAccountEnabled(enabled);
      if (!enabled) {
        // Disabling it ended this session too, so there is nothing left to
        // render from here.
        location.reload();
        return;
      }
      onnotice("The built-in account is on again.");
      await onchanged();
    } catch (err) {
      onerror((err as Error).message);
    } finally {
      toggling = false;
    }
  }
</script>

<!--
  One password box. The note is a separate parameter rather than markup in the
  label, so nothing here needs {@html} — a literal today is still a habit that
  travels to a value that is not one.
-->
{#snippet passwordBox(
  id: string,
  label: string,
  value: string,
  autocomplete: AutoFill,
  oninput: (v: string) => void,
  note?: string,
)}
  <div>
    <label class="block text-xs text-muted-foreground" for={id}>
      {label}
      {#if note}<span class="text-muted-foreground/60">· {note}</span>{/if}
    </label>
    <input
      {id}
      type="password"
      {autocomplete}
      {value}
      oninput={(e) => oninput(e.currentTarget.value)}
      class="{input} mt-1"
    />
  </div>
{/snippet}

<h4 class="mt-8 text-sm font-medium">Built-in account</h4>

{#if authState.local_managed}
  <p class="mt-1 max-w-xl text-xs leading-relaxed text-muted-foreground/70">
    The password comes from <span class="font-mono">SILT_PASSWORD_HASH</span>, so it is not this
    screen's to change. Unset that variable if you would rather manage it here.
  </p>
{:else if authState.setup_required}
  <p class="mt-1 max-w-xl text-xs leading-relaxed text-muted-foreground/70">
    This account has no password yet. Set one and you can sign in without your provider — useful for
    the day the provider is the thing that is down.
  </p>
  <div class="mt-3 max-w-md space-y-3">
    {@render passwordBox(
      "new-password",
      "Password",
      newPassword,
      "new-password",
      (v) => (newPassword = v),
      `at least ${minimum} characters`,
    )}
    <div>
      {@render passwordBox("confirm-password", "Confirm", confirmPassword, "new-password", (v) => (confirmPassword = v))}
      {#if mismatch}
        <p class="mt-1 text-xs text-amber-600 dark:text-amber-400">These do not match.</p>
      {/if}
    </div>
    <Button size="sm" onclick={setFirstPassword} disabled={!canSetFirst || changing}>
      {changing ? "Setting…" : "Set password"}
    </Button>
  </div>
{:else if authState.local_enabled}
  <div class="mt-3 max-w-md space-y-3">
    {@render passwordBox(
      "current-password",
      "Current password",
      currentPassword,
      "current-password",
      (v) => (currentPassword = v),
    )}
    {@render passwordBox(
      "new-password",
      "New password",
      newPassword,
      "new-password",
      (v) => (newPassword = v),
      `at least ${minimum} characters`,
    )}
    <div>
      {@render passwordBox("confirm-password", "Confirm", confirmPassword, "new-password", (v) => (confirmPassword = v))}
      {#if mismatch}
        <p class="mt-1 text-xs text-amber-600 dark:text-amber-400">These do not match.</p>
      {/if}
    </div>
    <Button size="sm" onclick={changePassword} disabled={!canChange || changing}>
      {changing ? "Changing…" : "Change password"}
    </Button>
    <p class="text-xs leading-relaxed text-muted-foreground/70">
      Changing it signs every other browser out, so doing this because you think it leaked also ends
      whatever leaked.
    </p>
  </div>
{:else}
  <p class="mt-1 max-w-xl text-xs leading-relaxed text-muted-foreground/70">
    Password sign-in is turned off for this account. It still exists, and a linked provider identity
    still reaches it.
  </p>
{/if}

{#if authState.oidc_enabled}
  <div class="mt-5">
    {#if authState.local_linked}
      <p class="text-xs leading-relaxed text-muted-foreground">
        Linked to a provider identity — signing in with it reaches this account, whatever the group
        allowlists say.
      </p>
      <Button variant="outline" size="sm" class="mt-2" onclick={unlink}>Unlink</Button>
    {:else}
      <p class="max-w-xl text-xs leading-relaxed text-muted-foreground">
        Link this account to your provider identity, and signing in there reaches the same account.
        That is what lets you turn the password off and keep the account.
      </p>
      <Button variant="outline" size="sm" class="mt-2" onclick={() => api.linkAccount()}>
        Link to my provider identity
      </Button>
    {/if}
  </div>
{/if}

<div class="mt-5">
  {#if authState.local_enabled}
    <Button
      variant="outline"
      size="sm"
      onclick={() => setEnabled(false)}
      disabled={toggling || (!authState.oidc_enabled && !authState.proxy_enabled)}
    >
      Turn the built-in account off
    </Button>
    {#if !authState.oidc_enabled && !authState.proxy_enabled}
      <p class="mt-1.5 max-w-xl text-xs text-muted-foreground/70">
        Not while it is the only way in. Configure a provider or a reverse proxy first.
      </p>
    {/if}
  {:else}
    <Button variant="outline" size="sm" onclick={() => setEnabled(true)} disabled={toggling}>
      Turn the built-in account on
    </Button>
  {/if}
</div>
