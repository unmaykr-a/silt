<script lang="ts">
  import { api } from "$lib/api/client";
  import { Button } from "$lib/components/ui/button";
  import SiltMark from "$lib/components/SiltMark.svelte";

  let { onAuthenticated }: { onAuthenticated: () => void } = $props();

  let password = $state("");
  let error = $state<string | null>(null);
  let busy = $state(false);

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
</script>

<div class="flex min-h-screen items-center justify-center bg-background px-6">
  <form class="w-full max-w-xs" onsubmit={submit}>
    <h1 class="flex items-center gap-2.5 text-2xl font-semibold tracking-tight">
      <SiltMark size={26} marker="#34d399" />
      Silt
    </h1>
    <p class="mt-1 text-sm text-muted-foreground">What settled on your stack, and when.</p>

    <label class="mt-8 block text-xs text-muted-foreground" for="password">Password</label>
    <input
      id="password"
      type="password"
      bind:value={password}
      autocomplete="current-password"
      class="mt-1 w-full rounded-md border border-border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-ring"
    />

    {#if error}
      <p class="mt-2 text-xs text-red-400">{error}</p>
    {/if}

    <div class="mt-4">
      <Button type="submit" disabled={busy || password === ""}>
        {busy ? "Signing in…" : "Sign in"}
      </Button>
    </div>
  </form>
</div>
