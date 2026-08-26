<script lang="ts">
  import { router, link } from "$lib/router.svelte";
  import { subscribe, api, type Project, type AuthState } from "$lib/api/client";
  import Login from "$lib/components/Login.svelte";
  import Sidebar from "$lib/components/Sidebar.svelte";
  import Timeline from "./routes/Timeline.svelte";
  import Project_ from "./routes/Project.svelte";
  import Service from "./routes/Service.svelte";
  import Diff from "./routes/Diff.svelte";
  import Files from "./routes/Files.svelte";
  import Settings from "./routes/Settings.svelte";

  type Status = "connecting" | "live" | "offline";

  let status = $state<Status>("connecting");
  let projects = $state<Project[]>([]);
  // Bumped on every server-sent event; screens depend on it to refetch.
  let reloadKey = $state(0);
  let theme = $state<"dark" | "light">("dark");
  let auth = $state<AuthState | null>(null);
  let navOpen = $state(false);

  const locked = $derived(auth !== null && auth.required && !auth.authenticated);

  async function refreshAuth() {
    try {
      auth = await api.authState();
    } catch {
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

    // A burst of events is common — a `compose up` across a stack, or the very
    // first boot discovering thirty projects at once. Coalesce the refetches
    // so the page does not thrash.
    let pending: ReturnType<typeof setTimeout> | null = null;
    const bump = () => {
      if (pending) return;
      pending = setTimeout(() => {
        pending = null;
        reloadKey++;
      }, 400);
    };

    const unsubscribe = subscribe({
      ready: () => (status = "live"),
      event: bump,
      "snapshot.changed": () => {
        bump();
        api.projects().then((p) => (projects = p)).catch(() => {});
      },
    });

    return () => {
      controller.abort();
      if (pending) clearTimeout(pending);
      unsubscribe();
      status = "offline";
    };
  });

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
  <div class="flex min-h-screen flex-col bg-background text-foreground">
    <header class="flex h-14 shrink-0 items-center gap-3 border-b border-border px-4">
      <button
        class="rounded-md p-1.5 text-muted-foreground hover:text-foreground md:hidden"
        onclick={() => (navOpen = !navOpen)}
        aria-label="Toggle navigation"
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M3 6h18M3 12h18M3 18h18" />
        </svg>
      </button>

      <a use:link href="/" class="text-base font-semibold tracking-tight">Silt</a>

      <div class="ml-auto flex items-center gap-4 text-xs">
        {#if auth?.required && auth.password_enabled}
          <button
            class="text-muted-foreground transition-colors hover:text-foreground"
            onclick={async () => {
              await api.logout();
              await refreshAuth();
            }}
          >
            Sign out
          </button>
        {/if}
        <button
          class="text-muted-foreground transition-colors hover:text-foreground"
          onclick={() => (theme = theme === "dark" ? "light" : "dark")}
        >
          {theme === "dark" ? "Light" : "Dark"}
        </button>
        <span class="flex items-center gap-1.5 text-muted-foreground" title="Live update stream">
          <span class="size-2 rounded-full {dotClass}" aria-hidden="true"></span>
          {status}
        </span>
      </div>
    </header>

    <div class="flex min-h-0 flex-1">
      <Sidebar {projects} bind:open={navOpen} />

      <!-- min-w-0 is what stops a wide table or a long path from pushing the
           whole page sideways; without it the body scrolls horizontally. -->
      <main class="min-w-0 flex-1 overflow-x-hidden px-6 py-6">
        <div class="mx-auto max-w-[100rem]">
          {#if route.name === "timeline"}
            <Timeline {reloadKey} {projects} />
          {:else if route.name === "project"}
            <Project_ projectId={route.projectId} {reloadKey} />
          {:else if route.name === "service"}
            <Service projectId={route.projectId} service={route.service} />
          {:else if route.name === "files"}
            <Files projectId={route.projectId} initialPath={route.path} />
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
        </div>
      </main>
    </div>
  </div>
{/if}
