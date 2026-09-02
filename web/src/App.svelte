<script lang="ts">
  import { router, link } from "$lib/router.svelte";
  import { subscribe, api, type Project, type AuthState, type StreamStatus } from "$lib/api/client";
  import { prefs } from "$lib/prefs.svelte";
  import Login from "$lib/components/Login.svelte";
  import Sidebar from "$lib/components/Sidebar.svelte";
  import SiltMark from "$lib/components/SiltMark.svelte";
  import SearchBox from "$lib/components/SearchBox.svelte";
  import StatusMenu from "$lib/components/StatusMenu.svelte";
  import Changelog from "$lib/components/Changelog.svelte";
  import Timeline from "./routes/Timeline.svelte";
  import Projects from "./routes/Projects.svelte";
  import Search from "./routes/Search.svelte";
  import Project_ from "./routes/Project.svelte";
  import Service from "./routes/Service.svelte";
  import Diff from "./routes/Diff.svelte";
  import Files from "./routes/Files.svelte";
  import Settings from "./routes/Settings.svelte";

  let status = $state<StreamStatus>("connecting");
  // When the current status began, so the menu can say how long it has been
  // live rather than only that it is.
  let statusSince = $state(Date.now());

  // Deliberately a plain variable, not $state.
  //
  // setStatus is called synchronously while the subscription effect is being
  // set up. Reading a rune there would make the effect depend on it, and the
  // very next line writes it — so the effect re-runs, tears down the
  // EventSource, opens another, reports "connecting" again, and loops. The
  // symptom was a status stuck on "Reconnecting…" over a server that was
  // answering perfectly, and a page opening a new stream several times a
  // second. Comparison state that a subscription callback touches must stay
  // outside the reactive graph.
  let lastStatus: StreamStatus = "connecting";

  // When Silt last said anything at all, heartbeat included, and when it last
  // said something had changed. Two different questions: an idle host is
  // silent about changes for hours and should still be able to prove it is
  // being watched.
  let lastFrameAt = $state(Date.now());
  let lastChangeAt = $state(0);

  function setStatus(next: StreamStatus) {
    if (next !== lastStatus) {
      lastStatus = next;
      statusSince = Date.now();
    }
    status = next;
  }
  let changelogOpen = $state(false);
  let projects = $state<Project[]>([]);
  // Bumped on every server-sent event; screens depend on it to refetch.
  let reloadKey = $state(0);
  let auth = $state<AuthState | null>(null);
  // Separate from `auth` being null, which is also what a failed request
  // leaves behind. Until the first answer arrives Silt does not know whether
  // it is locked, and rendering the app on that guess is what made an
  // unauthenticated reload flash the whole UI before replacing it with a
  // login form.
  let authKnown = $state(false);
  let navOpen = $state(false);

  const locked = $derived(auth !== null && auth.required && !auth.authenticated);

  async function refreshAuth() {
    try {
      auth = await api.authState();
    } catch {
      // Silt is unreachable rather than refusing: show the app and let the
      // screens report their own errors, which is more use than a login form
      // for a server that is not answering.
      auth = null;
    } finally {
      authKnown = true;
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

    const unsubscribe = subscribe(
      {
        event: () => {
          lastChangeAt = Date.now();
          bump();
        },
        "snapshot.changed": () => {
          lastChangeAt = Date.now();
          bump();
          api.projects().then((p) => (projects = p)).catch(() => {});
        },
      },
      {
        // The connection reports itself now. It used to go live on the first
        // `ready` frame and stay there: a server restart, a dropped network or
        // a closed laptop lid all left a green dot over a page that had
        // stopped updating.
        status: setStatus,
        activity: (at) => (lastFrameAt = at),
      },
    );

    return () => {
      controller.abort();
      if (pending) clearTimeout(pending);
      unsubscribe();
    };
  });

  const route = $derived(router.current);

  async function signOut() {
    await api.logout();
    await refreshAuth();
  }

  // The top-level destinations. A project screen still counts as Projects, so
  // the navigation keeps telling you where you are once you have drilled in.
  const SECTIONS = [
    { href: "/", label: "Timeline", matches: ["timeline"], icon: "timeline" },
    {
      href: "/projects",
      label: "Projects",
      matches: ["projects", "project", "service", "files", "diff"],
      icon: "projects",
    },
    { href: "/settings", label: "Settings", matches: ["settings"], icon: "settings" },
  ];

  const isActive = $derived((matches: string[]) => matches.includes(route.name));

  // Two shapes for the same navigation. The top bar keeps the content full
  // width, which suits the timeline; the rail puts everything in one column,
  // which is what authentik and Dockhand do and what someone running thirty
  // stacks tends to prefer. Neither is right for everyone, so it is a setting.
  const side = $derived(prefs.layout === "side");

  // In the top layout the rail is project navigation, so it has nothing to add
  // on the two screens that are not about a project. In the side layout it is
  // the navigation, so it is always there.
  const showRail = $derived(
    side || (route.name !== "settings" && route.name !== "projects" && route.name !== "search"),
  );
</script>

{#snippet navIcon(name: string)}
  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
       stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" class="shrink-0">
    {#if name === "timeline"}
      <path d="M3 12h4l3-7 4 14 3-7h4" />
    {:else if name === "projects"}
      <rect x="3" y="3" width="7" height="7" rx="1.5" />
      <rect x="14" y="3" width="7" height="7" rx="1.5" />
      <rect x="3" y="14" width="7" height="7" rx="1.5" />
      <rect x="14" y="14" width="7" height="7" rx="1.5" />
    {:else}
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.6a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
    {/if}
  </svg>
{/snippet}

{#snippet railNav()}
  <nav class="space-y-0.5" aria-label="Sections">
    {#each SECTIONS as section (section.href)}
      <a
        use:link
        href={section.href}
        onclick={() => (navOpen = false)}
        class="flex items-center gap-2.5 rounded-md px-2.5 py-2 text-sm transition-colors
               {isActive(section.matches)
          ? 'bg-secondary text-secondary-foreground'
          : 'text-muted-foreground hover:bg-secondary/50 hover:text-foreground'}"
      >
        {@render navIcon(section.icon)}
        {section.label}
      </a>
    {/each}
  </nav>
{/snippet}

{#if !authKnown}
  <!-- One paint of the mark rather than the whole application. The request it
       is waiting on is local and answers in a few milliseconds, so this is
       almost never seen — and when Silt is slow to answer, a quiet mark is a
       better wait than a UI that is about to be taken away. -->
  <div class="flex min-h-screen items-center justify-center bg-background">
    <SiltMark size={30} class="animate-pulse text-muted-foreground/40" marker="currentColor" />
    <span class="sr-only">Loading Silt</span>
  </div>
{:else if locked}
  <Login onAuthenticated={refreshAuth} authState={auth} />
{:else}
  <!-- h-screen with the overflow contained here is what lets the rail and the
       content scroll separately. Without it the tallest column decides the
       page height and the rail scrolls the whole document with it. -->
  <div class="flex h-screen flex-col overflow-hidden bg-background text-foreground">
    <header class="flex h-12 shrink-0 items-center gap-2 border-b border-border px-3 sm:px-4">
      {#if showRail}
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

      <a
        use:link
        href="/"
        class="mr-2 flex items-center gap-2 text-[15px] font-semibold tracking-tight {side ? 'w-[13.5rem]' : ''}"
      >
        <SiltMark size={17} marker="#34d399" />
        <!-- The mark alone identifies Silt; the word is what does not fit. -->
        <span class="hidden sm:inline">Silt</span>
      </a>

      {#if !side}
        <!-- Icons only below sm. The mark, three labelled links, a search box
             and the status button do not fit a 390px header, and the overflow
             was invisible until opening the menu scrolled the page sideways to
             bring the menu into view. Hiding the nav outright was the other
             option and a worse one: on Settings and Projects there is no rail
             in this layout, so a phone would have had no navigation at all. -->
        <nav class="flex items-center gap-0.5" aria-label="Sections">
          {#each SECTIONS as section (section.href)}
            <a
              use:link
              href={section.href}
              onclick={() => (navOpen = false)}
              title={section.label}
              aria-label={section.label}
              class="flex items-center gap-1.5 rounded-md px-2 py-1.5 text-sm transition-colors sm:px-2.5
                     {isActive(section.matches)
                ? 'bg-secondary text-secondary-foreground'
                : 'text-muted-foreground hover:bg-secondary/50 hover:text-foreground'}"
            >
              <span class="sm:hidden">{@render navIcon(section.icon)}</span>
              <span class="hidden sm:inline">{section.label}</span>
            </a>
          {/each}
        </nav>
      {/if}

      <!-- Search stays out here: it is the one control in this corner you
           reach for constantly, and `/` has to hit something visible.
           Everything else — connection, version, theme, identity, sign out —
           is behind one button. Five controls of five shapes at the same
           weight was a row of widgets, not a header. -->
      <div class="ml-auto flex items-center gap-1.5 text-xs">
        <SearchBox />
        <StatusMenu
          {status}
          since={statusSince}
          {lastFrameAt}
          {lastChangeAt}
          {auth}
          onSignOut={signOut}
          onShowChangelog={() => (changelogOpen = true)}
        />
      </div>
    </header>

    <div class="flex min-h-0 flex-1">
      {#if showRail}
        <Sidebar {projects} bind:open={navOpen} nav={side ? railNav : undefined} />
      {/if}

      <!-- min-w-0 is what stops a wide table or a long path from pushing the
           whole page sideways; without it the body scrolls horizontally. -->
      <main class="min-w-0 flex-1 overflow-y-auto overflow-x-hidden px-5 py-5 sm:px-6">
        <div class="mx-auto max-w-[100rem]">
          {#if route.name === "timeline"}
            <Timeline {reloadKey} {projects} />
          {:else if route.name === "projects"}
            <Projects {reloadKey} />
          {:else if route.name === "search"}
            <Search query={route.query} />
          {:else if route.name === "project"}
            <Project_ projectId={route.projectId} {reloadKey} />
          {:else if route.name === "service"}
            <Service projectId={route.projectId} service={route.service} {reloadKey} />
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

    <!-- Mounted at the shell rather than inside the menu, so opening it does
         not depend on the menu staying open. -->
    <Changelog bind:open={changelogOpen} />
  </div>
{/if}
