<script lang="ts">
  import { router, link } from "$lib/router.svelte";
  import { subscribe, api, type Project, type AuthState } from "$lib/api/client";
  import Login from "$lib/components/Login.svelte";
  import Timeline from "./routes/Timeline.svelte";
  import Project_ from "./routes/Project.svelte";
  import Service from "./routes/Service.svelte";
  import Diff from "./routes/Diff.svelte";
  import Settings from "./routes/Settings.svelte";

  type Status = "connecting" | "live" | "offline";

  let status = $state<Status>("connecting");
  let projects = $state<Project[]>([]);
  // Bumped on every server-sent event; screens depend on it to refetch.
  let reloadKey = $state(0);
  let theme = $state<"dark" | "light">("dark");
  let auth = $state<AuthState | null>(null);

  // Gate everything on the auth state so an unauthenticated visitor sees a
  // login form rather than a page of failed requests.
  const locked = $derived(auth !== null && auth.required && !auth.authenticated);

  async function refreshAuth() {
    try {
      auth = await api.authState();
    } catch {
      // If even this fails the server is unreachable; the status dot says so.
      auth = null;
    }
  }

  $effect(() => {
    void refreshAuth();
  });

  $effect(() => {
    if (locked) return;
    const controller = new AbortController();
    api.projects(controller.signal).then((p) => (projects = p)).catch(() => {});

    const unsubscribe = subscribe({
      ready: () => (status = "live"),
      event: () => reloadKey++,
      "snapshot.changed": () => {
        reloadKey++;
        api.projects().then((p) => (projects = p)).catch(() => {});
      },
    });

    return () => {
      controller.abort();
      unsubscribe();
      status = "offline";
    };
  });

  // Dark by default, light available (PROJECT.md Section 9). Stored per
  // browser; a missing or unreadable value just means the default.
  $effect(() => {
    try {
      const saved = localStorage.getItem("silt.theme");
      if (saved === "light" || saved === "dark") theme = saved;
    } catch {
      // Private windows and blocked site data both throw here.
    }
  });

  $effect(() => {
    document.documentElement.classList.toggle("dark", theme === "dark");
    try {
      localStorage.setItem("silt.theme", theme);
    } catch {
      // Non-fatal: the choice just will not persist.
    }
  });

  const route = $derived(router.current);
  const dotClass = $derived(
    status === "live" ? "bg-emerald-400" : status === "connecting" ? "bg-zinc-500" : "bg-red-400",
  );
</script>

{#if locked}
  <Login onAuthenticated={refreshAuth} />
{:else}
<div class="min-h-screen bg-background text-foreground">
  <header class="border-b border-border">
    <div class="mx-auto flex max-w-5xl flex-wrap items-center gap-x-6 gap-y-2 px-6 py-4">
      <a use:link href="/" class="text-lg font-semibold tracking-tight">Silt</a>

      <nav class="flex items-center gap-4 text-sm">
        <a
          use:link
          href="/"
          class="underline-offset-4 hover:underline {route.name === 'timeline' ? 'text-foreground' : 'text-muted-foreground'}"
        >
          Timeline
        </a>
        {#each projects as project (project.id)}
          <a
            use:link
            href="/projects/{project.id}"
            class="underline-offset-4 hover:underline {(route.name === 'project' || route.name === 'service') && route.projectId === project.id
              ? 'text-foreground'
              : 'text-muted-foreground'}"
          >
            {project.name}
          </a>
        {/each}
        <a
          use:link
          href="/settings"
          class="underline-offset-4 hover:underline {route.name === 'settings' ? 'text-foreground' : 'text-muted-foreground'}"
        >
          Settings
        </a>
      </nav>

      <div class="ml-auto flex items-center gap-4">
        {#if auth?.required && auth.password_enabled}
          <button
            class="text-xs text-muted-foreground transition-colors hover:text-foreground"
            onclick={async () => {
              await api.logout();
              await refreshAuth();
            }}
          >
            Sign out
          </button>
        {/if}
        <button
          class="text-xs text-muted-foreground transition-colors hover:text-foreground"
          onclick={() => (theme = theme === "dark" ? "light" : "dark")}
          aria-label="Toggle theme"
        >
          {theme === "dark" ? "Light" : "Dark"}
        </button>
        <span class="flex items-center gap-1.5 text-xs text-muted-foreground" title="Live update stream">
          <span class="size-2 rounded-full {dotClass}" aria-hidden="true"></span>
          {status}
        </span>
      </div>
    </div>
  </header>

  <main class="mx-auto max-w-5xl px-6 py-8">
    {#if route.name === "timeline"}
      <Timeline {reloadKey} />
    {:else if route.name === "project"}
      <Project_ projectId={route.projectId} {reloadKey} />
    {:else if route.name === "service"}
      <Service projectId={route.projectId} service={route.service} />
    {:else if route.name === "diff"}
      <Diff from={route.from} to={route.to} projectId={route.projectId} />
    {:else if route.name === "settings"}
      <Settings />
    {:else}
      <div class="py-16 text-center">
        <p class="text-sm text-muted-foreground">No such page: <code>{route.path}</code></p>
        <a use:link href="/" class="mt-2 inline-block text-sm underline underline-offset-4">
          Back to the timeline
        </a>
      </div>
    {/if}
  </main>
</div>
{/if}
