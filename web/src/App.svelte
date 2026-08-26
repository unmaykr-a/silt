<script lang="ts">
  import { router, link } from "$lib/router.svelte";
  import { subscribe, api, type Project, type AuthState } from "$lib/api/client";
  import Login from "$lib/components/Login.svelte";
  import Sidebar from "$lib/components/Sidebar.svelte";
  import ThemeToggle from "$lib/components/ThemeToggle.svelte";
  import VersionButton from "$lib/components/VersionButton.svelte";
  import Timeline from "./routes/Timeline.svelte";
  import Projects from "./routes/Projects.svelte";
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

  const route = $derived(router.current);
  const dotClass = $derived(
    status === "live" ? "bg-emerald-400" : status === "connecting" ? "bg-zinc-500" : "bg-red-400",
  );

  // The top-level destinations. A project screen still counts as Projects, so
  // the header keeps telling you where you are once you have drilled in.
  const SECTIONS = [
    { href: "/", label: "Timeline", matches: ["timeline"] },
    { href: "/projects", label: "Projects", matches: ["projects", "project", "service", "files", "diff"] },
    { href: "/settings", label: "Settings", matches: ["settings"] },
  ];

  function sectionClass(matches: string[]): string {
    return matches.includes(route.name)
      ? "bg-secondary text-secondary-foreground"
      : "text-muted-foreground hover:bg-secondary/50 hover:text-foreground";
  }

  // The sidebar is project navigation, so it has nothing to add on the two
  // screens that are not about a project.
  const showSidebar = $derived(route.name !== "settings" && route.name !== "projects");
</script>

{#if locked}
  <Login onAuthenticated={refreshAuth} />
{:else}
  <div class="flex min-h-screen flex-col bg-background text-foreground">
    <header class="flex h-14 shrink-0 items-center gap-2 border-b border-border px-3 sm:px-4">
      {#if showSidebar}
        <button
          class="rounded-md p-1.5 text-muted-foreground hover:text-foreground md:hidden"
          onclick={() => (navOpen = !navOpen)}
          aria-label="Toggle navigation"
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M3 6h18M3 12h18M3 18h18" />
          </svg>
        </button>
      {/if}

      <a use:link href="/" class="mr-2 flex items-center gap-2 text-base font-semibold tracking-tight">
        <!-- Layers, and the moment one of them was marked: strata is what Silt
             draws, and the marker is the change it recorded. -->
        <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true" class="shrink-0">
          <g fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
            <path d="M3 7h18" opacity="0.4" />
            <path d="M3 12h18" opacity="0.7" />
            <path d="M3 17h18" opacity="0.4" />
          </g>
          <circle cx="16" cy="12" r="3" class="fill-emerald-400" />
        </svg>
        Silt
      </a>

      <nav class="flex items-center gap-0.5" aria-label="Sections">
        {#each SECTIONS as section (section.href)}
          <a
            use:link
            href={section.href}
            onclick={() => (navOpen = false)}
            class="rounded-md px-2.5 py-1.5 text-sm transition-colors {sectionClass(section.matches)}"
          >
            {section.label}
          </a>
        {/each}
      </nav>

      <div class="ml-auto flex items-center gap-1 text-xs">
        <span
          class="mr-1 hidden items-center gap-1.5 text-muted-foreground sm:flex"
          title="Live update stream"
        >
          <span class="size-2 rounded-full {dotClass}" aria-hidden="true"></span>
          {status}
        </span>
        <VersionButton />
        <ThemeToggle />
        {#if auth?.required && auth.password_enabled}
          <button
            class="rounded-md px-2 py-1 text-muted-foreground transition-colors hover:bg-secondary/60 hover:text-foreground"
            onclick={async () => {
              await api.logout();
              await refreshAuth();
            }}
          >
            Sign out
          </button>
        {/if}
      </div>
    </header>

    <div class="flex min-h-0 flex-1">
      {#if showSidebar}
        <Sidebar {projects} bind:open={navOpen} />
      {/if}

      <!-- min-w-0 is what stops a wide table or a long path from pushing the
           whole page sideways; without it the body scrolls horizontally. -->
      <main class="min-w-0 flex-1 overflow-x-hidden px-6 py-6">
        <div class="mx-auto max-w-[100rem]">
          {#if route.name === "timeline"}
            <Timeline {reloadKey} {projects} />
          {:else if route.name === "projects"}
            <Projects {reloadKey} />
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
