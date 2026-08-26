<script lang="ts">
  import { api, type AuthState } from "$lib/api/client";
  import { Button } from "$lib/components/ui/button";
  import SiltMark from "$lib/components/SiltMark.svelte";

  // Not named `state`: that would shadow the $state rune inside this file.
  let { onAuthenticated, authState }: { onAuthenticated: () => void; authState: AuthState | null } =
    $props();

  let password = $state("");
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

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    try {
      await api.login(password);
      password = "";
      error = null;
      onAuthenticated();
    } catch (err) {
      error = (err as Error).message;
    } finally {
      busy = false;
    }
  }

  const both = $derived(!!authState?.oidc_enabled && !!authState?.password_enabled);
  // Where the button will send them, so it is not a leap of faith.
  const issuerHost = $derived.by(() => {
    if (!authState?.oidc_issuer) return "";
    try {
      return new URL(authState.oidc_issuer).host;
    } catch {
      return authState.oidc_issuer;
    }
  });
</script>

<div class="flex min-h-screen items-center justify-center bg-background px-6">
  <div class="w-full max-w-xs">
    <h1 class="flex items-center gap-2.5 text-2xl font-semibold tracking-tight">
      <SiltMark size={26} marker="#34d399" />
      Silt
    </h1>
    <p class="mt-1 text-sm text-muted-foreground">What settled on your stack, and when.</p>

    {#if error}
      <p class="mt-6 rounded-md border border-red-500/40 bg-red-500/10 px-3 py-2 text-xs text-red-500 dark:text-red-300">
        {error}
      </p>
    {/if}

    {#if authState?.oidc_enabled}
      <div class="mt-8">
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

    {#if authState?.password_enabled}
      <form class={both ? "" : "mt-8"} onsubmit={submit}>
        <label class="block text-xs text-muted-foreground" for="password">Password</label>
        <input
          id="password"
          type="password"
          bind:value={password}
          autocomplete="current-password"
          class="mt-1 w-full rounded-md border border-border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-ring"
        />
        <div class="mt-4">
          <Button type="submit" variant={authState?.oidc_enabled ? "outline" : "default"} disabled={busy || password === ""}>
            {busy ? "Signing in…" : "Sign in"}
          </Button>
        </div>
      </form>
    {/if}

    {#if authState?.required && !authState.password_enabled && !authState.oidc_enabled}
      <!-- Forward auth only: there is nothing to type, and the proxy in front
           should have asked already. Saying so beats an empty screen. -->
      <p class="mt-8 rounded-md border border-border bg-secondary/30 px-3 py-2 text-xs leading-relaxed text-muted-foreground">
        Silt expects your reverse proxy to authenticate you and pass the identity along.
        This request arrived without one — check the proxy in front of Silt.
      </p>
    {/if}
  </div>
</div>
