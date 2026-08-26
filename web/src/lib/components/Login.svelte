<script lang="ts">
  import { api, type AuthState } from "$lib/api/client";
  import { Button } from "$lib/components/ui/button";
  import SiltMark from "$lib/components/SiltMark.svelte";

  // Not named `state`: that would shadow the $state rune inside this file.
  let { onAuthenticated, authState }: { onAuthenticated: () => void; authState: AuthState | null } =
    $props();

  let password = $state("");
  let confirm = $state("");
  let error = $state<string | null>(null);
  let busy = $state(false);

  // A failed provider login comes back as a redirect carrying the reason,
  // because the browser arrived by navigation and a JSON error would be a dead
  // end. Read it once and clean the URL, so a reload does not replay it.
  $effect(() => {
    const params = new URLSearchParams(location.search);
    const reason = params.get("login_error");
    if (!reason) return;
    error = reason;
    params.delete("login_error");
    const query = params.toString();
    history.replaceState({}, "", location.pathname + (query ? `?${query}` : ""));
  });

  const setup = $derived(!!authState?.setup_required);
  const minimum = $derived(authState?.min_password_length ?? 10);
  const showOIDC = $derived(!!authState?.oidc_enabled && !setup);
  const showPassword = $derived(!!authState?.password_enabled && !setup);
  const both = $derived(showOIDC && showPassword);
  // Forward auth only, and the proxy did not assert anyone. There is nothing
  // to type; saying so beats an empty screen with a lone heading.
  const proxyOnly = $derived(!!authState?.required && !setup && !showOIDC && !showPassword);

  // Where the button will send them, so it is not a leap of faith.
  const issuerHost = $derived.by(() => {
    if (!authState?.oidc_issuer) return "";
    try {
      return new URL(authState.oidc_issuer).host;
    } catch {
      return authState.oidc_issuer;
    }
  });

  const tooShort = $derived(password.length > 0 && password.length < minimum);
  const mismatch = $derived(confirm.length > 0 && confirm !== password);
  const canSetUp = $derived(password.length >= minimum && confirm === password);

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    try {
      if (setup) {
        await api.setupAccount(password);
      } else {
        await api.login(password);
      }
      password = "";
      confirm = "";
      error = null;
      onAuthenticated();
    } catch (err) {
      error = (err as Error).message;
    } finally {
      busy = false;
    }
  }

  const input =
    "mt-1 w-full rounded-md border border-border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-ring";
</script>

<div class="flex min-h-screen items-center justify-center bg-background px-6 py-10">
  <div class="w-full max-w-sm">
    <h1 class="flex items-center gap-2.5 text-2xl font-semibold tracking-tight">
      <SiltMark size={26} marker="#34d399" />
      Silt
    </h1>
    <p class="mt-1 text-sm text-muted-foreground">
      {setup ? "Nobody has set this up yet. Choose a password." : "What settled on your stack, and when."}
    </p>

    {#if error}
      <p class="mt-6 rounded-md border border-red-500/40 bg-red-500/10 px-3 py-2 text-xs leading-relaxed text-red-500 dark:text-red-300">
        {error}
      </p>
    {/if}

    {#if setup}
      <!-- First run. Everything else is refused until this succeeds, so this
           is the only thing on screen. -->
      <form class="mt-7" onsubmit={submit}>
        <label class="block text-xs text-muted-foreground" for="password">
          Password <span class="text-muted-foreground/60">· at least {minimum} characters</span>
        </label>
        <input
          id="password"
          type="password"
          bind:value={password}
          autocomplete="new-password"
          autofocus
          class={input}
        />
        {#if tooShort}
          <p class="mt-1 text-xs text-amber-600 dark:text-amber-400">
            {minimum - password.length} more to go.
          </p>
        {/if}

        <label class="mt-4 block text-xs text-muted-foreground" for="confirm">Confirm</label>
        <input id="confirm" type="password" bind:value={confirm} autocomplete="new-password" class={input} />
        {#if mismatch}
          <p class="mt-1 text-xs text-amber-600 dark:text-amber-400">These do not match.</p>
        {/if}

        <div class="mt-5">
          <Button type="submit" class="w-full" disabled={busy || !canSetUp}>
            {busy ? "Setting up…" : "Set password and continue"}
          </Button>
        </div>

        <p class="mt-4 text-xs leading-relaxed text-muted-foreground/70">
          This is the built-in administrator. You can change it, link it to an identity provider,
          or turn it off later under Settings → Security.
        </p>
      </form>
    {:else}
      {#if showOIDC}
        <div class="mt-7">
          <Button class="w-full" onclick={() => api.oidcLogin()}>
            Sign in{#if issuerHost}&nbsp;with {issuerHost}{/if}
          </Button>
        </div>
      {/if}

      {#if both}
        <div class="my-6 flex items-center gap-3 text-[11px] uppercase tracking-wide text-muted-foreground/50">
          <span class="h-px flex-1 bg-border"></span>
          or
          <span class="h-px flex-1 bg-border"></span>
        </div>
      {/if}

      {#if showPassword}
        <form class={both ? "" : "mt-7"} onsubmit={submit}>
          <label class="block text-xs text-muted-foreground" for="password">Password</label>
          <input
            id="password"
            type="password"
            bind:value={password}
            autocomplete="current-password"
            class={input}
          />
          <div class="mt-4">
            <Button
              type="submit"
              class="w-full"
              variant={showOIDC ? "outline" : "default"}
              disabled={busy || password === ""}
            >
              {busy ? "Signing in…" : "Sign in"}
            </Button>
          </div>
        </form>
      {/if}

      {#if proxyOnly}
        <p class="mt-7 rounded-md border border-border bg-secondary/30 px-3 py-2.5 text-xs leading-relaxed text-muted-foreground">
          Silt expects your reverse proxy to authenticate you and pass the identity along in a
          header. This request arrived without one — check the proxy in front of Silt, and that
          <span class="font-mono">SILT_TRUSTED_PROXIES</span> includes its address.
        </p>
      {/if}
    {/if}
  </div>
</div>
