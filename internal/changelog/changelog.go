// Package changelog is Silt's release history.
//
// The history lives here as Go data rather than as a markdown file parsed at
// runtime, for two reasons. The UI wants it structured — a release, a date, a
// set of entries grouped by kind — and parsing prose to get that back is a
// guessing game. And the root CHANGELOG.md is generated from this, with a test
// that fails when the two drift, so there is one source of truth rather than
// two that agree until someone edits the wrong one.
package changelog

// Kind groups the entries within a release.
type Kind string

const (
	Added    Kind = "added"
	Changed  Kind = "changed"
	Fixed    Kind = "fixed"
	Removed  Kind = "removed"
	Security Kind = "security"
)

// Order is the order kinds are rendered in.
var Order = []Kind{Added, Changed, Fixed, Security, Removed}

// Entry is one line of a release.
type Entry struct {
	Kind Kind   `json:"kind"`
	Text string `json:"text"`
}

// Release is one published version.
type Release struct {
	Version string  `json:"version"`
	Date    string  `json:"date"`
	Summary string  `json:"summary,omitempty"`
	Entries []Entry `json:"entries"`
}

// Releases is the history, newest first.
var Releases = []Release{
	{
		Version: "0.13.0",
		Date:    "2026-09-03",
		Summary: "A demo you can click through, and the pipeline finally has tests.",
		Entries: []Entry{
			{Added, "A live demo at https://unmaykr-a.github.io/silt/ — the whole UI, running against captured data, with no server behind it. Every screen, every graph and every control is the code that ships; only the transport differs, so what you click through is what you get. Writes are refused rather than faked, and the timestamps are shifted onto your clock so it never reads as an abandoned deployment."},
			{Added, "`make demo-site` builds it and `make demo-site-verify` drives the result in a browser, failing if any screen has a request the capture missed — the demo's one failure mode is a blank panel nothing else would notice."},
			{Added, "Tests for the collection pipeline, end to end: an observation that becomes a snapshot and a broadcast, an unchanged one that stays silent, a runtime-only change reaching the browser (the bug 0.10.0 fixed, now pinned), an image change counting as a configuration change, a secret that does not survive the pipeline, a container that vanishes mid-snapshot, and discovery grouping by project. The package that had shipped a real bug was the one at 22% coverage; it is now the best-covered path in the collector."},
			{Added, "The fake Docker engine the tests run against is now a package of its own, with container inspection and image inspection added. There was one copy in the Docker tests and none where the pipeline is; there is now one implementation both use."},
			{Added, "Tests for route parsing, which decides what every deep link resolves to and had none."},
			{Fixed, "A browser started with LANG=C, LANG=POSIX or LANG=en_US@posix rendered nothing at all. Those report a locale of `en-US@posix`, which is not a valid language tag; the charting library builds a number formatter from it as it loads, so the import threw, so the whole bundle threw. A blank page, from a locale setting with nothing to do with charts. An invalid tag is now repaired at the point it would throw, and date formatting falls back rather than failing."},
			{Changed, "The theme control sits on one line with its label, sized to itself. Stretched to the panel it was a bordered box inside a bordered box — the only hard rectangle in a menu that is otherwise flowing text, and it read as wedged against the edge rather than placed."},
			{Changed, "Links are written app-relative and rewritten onto wherever Silt is mounted, so ctrl-click, middle-click and the browser's own status bar all agree with what a plain click does."},
		},
	},
	{
		Version: "0.12.0",
		Date:    "2026-09-03",
		Summary: "The testing round becomes a test suite.",
		Entries: []Entry{
			{Added, "An end-to-end suite: 117 checks driving a real binary against a seeded database. Every route, at four widths, in three configurations, asserting no console error, no horizontal overflow and something on the page — plus the controls, the settings sections, the sliding markers, and a change arriving over the live stream with no reload. The last round found a page that died only where nothing is selected; that was a one-off script, and this is the same sweep on every push."},
			{Added, "`make demo` builds a populated database without a Docker host — fourteen projects covering every container state, an unapplied compose edit, and enough history for the graphs to have shape. Development and the test suite now share it instead of a seeder written and deleted each time."},
			{Added, "Tests for how Docker's inspect response is read: exit codes only from stopped containers, a zero exit code kept distinct from none, an absent healthcheck kept distinct from a healthy one, and a missing or unparseable start time left unset rather than landing in 1970. That function carried the newest logic in the project and had no tests at all."},
			{Added, "Tests for the severity a Docker event lands with, which decides whether it reaches the error count and whether a notification filter lets it through. An action a future Docker adds stays informational rather than turning the timeline red on an engine upgrade."},
		},
	},
	{
		Version: "0.11.0",
		Date:    "2026-09-03",
		Summary: "A testing round, and the things it found.",
		Entries: []Entry{
			{Security, "SILT_KEEP_KEYS=* silently turned redaction off. Keep keys are matched as globs, so a bare * matched every environment variable on the host and stored all of them — passwords included — in cleartext, with no warning anywhere. `**`, `?*` and `[A-Z]*` did the same. Patterns are now validated to what the documentation always promised: a name, optionally with a single * at one end. A pattern that keeps more than it names is refused at startup and on the settings screen, and one that reaches the matcher another way is dropped rather than obeyed."},
			{Fixed, "A typo in SILT_NOTIFY_ON was accepted and then matched nothing, so `image` instead of `image_id` meant \"never notify\" with no error — discovered during the outage the notification was for. Unknown change kinds are now refused, and the error lists the real ones."},
			{Fixed, "A snapshot where only a compose file changed showed in the project's list as one where nothing had happened. Silt recorded that from the first release and never reported it; it now reads \"file edited\" in the drift colour."},
			{Fixed, "SILT_BASE_URL was not validated. It becomes the link in a notification, so a value that is not a URL produced a link that went nowhere, in the one message you needed to work."},
			{Added, "The section links slide their marker like every other selection in Silt, and follow a drill-down: a service page still shows Projects as current."},
			{Fixed, "Visiting search or an unknown URL killed the page. Nothing is selected there, and the marker measurement merged into its own previous value — so the effect depended on its own output and Svelte aborted after too many updates. Found by sweeping every route at five widths in three configurations, which is now how this gets checked."},
		},
	},
	{
		Version: "0.10.0",
		Date:    "2026-09-02",
		Summary: "Live updates actually arrive, restarts stop mattering forever, and things move.",
		Entries: []Entry{
			{Fixed, "Live updates did not reach the project screens. Silt broadcast only when a project's *configuration* changed, on the reasoning that a runtime change is already covered by the Docker event that caused it. That held while the UI showed configuration; it stopped holding when the project screens started showing running counts, unhealthy and restarting. It was wrong twice: the Docker event goes out immediately but the snapshot it triggers is written two seconds later, so a browser refetching on it read the state from before the change — and the interval sweep emits no Docker event at all. Silt now broadcasts after any snapshot it actually wrote."},
			{Fixed, "A service page never live-updated at all; it was the one screen the refresh key was not wired into."},
			{Added, "The status menu says when Silt was last heard from, and when it last reported a change. Two different questions: an idle host is silent about changes for hours and should still be able to prove it is being watched."},
			{Changed, "The keep-alive frame is a named event rather than an SSE comment. A comment stops proxies closing an idle connection, which was its job, but browsers discard it without telling anyone — so a live-but-quiet connection and a wedged one looked identical, which is how the indicator managed to lie."},
			{Changed, "Restarts stop counting after a day. Docker's counter never resets, so one blip three months ago pinned its stack to the attention list forever, and a list that is permanently non-empty is one people stop reading. The count is still shown, in grey, with when it last happened; the service page keeps the full history."},
			{Added, "Each project has its own activity graph, over 24 hours to 90 days. The fleet timeline answers what happened on the host; standing on a project and having to go back and filter to ask it about the one stack in front of you was the gap."},
			{Added, "Selection markers slide. Time ranges, project sort, theme and the new project range all share one segmented control whose highlight moves between options instead of appearing somewhere else — a marker that jumps gives no sense of which way you went."},
		},
	},
	{
		Version: "0.9.0",
		Date:    "2026-09-02",
		Summary: "A header with two controls instead of five, and a record of who changed Silt.",
		Entries: []Entry{
			{Changed, "The header's right side was five controls of five shapes at the same weight — search, a status dot, a version button, a theme toggle and a sign-out button — sharing one corner. Search stays, because it is the one you reach for constantly. Everything else is behind a single button, where each of them has room to say more than it could as an icon."},
			{Fixed, "The live indicator lied. It turned green on the first frame and stayed green through a server restart, a dropped network and a closed laptop lid, because the connection's own open and error events were never observed. It now reports reconnecting and offline, and says how long it has been connected."},
			{Added, "A third theme setting: follow the system. The old toggle read your system preference once and then pinned light or dark forever, so a desktop that switches at sunset had to be followed by hand and there was no way back."},
			{Changed, "The changelog's category markers were fixed-width blocks of colour down the left edge — the loudest thing on a screen where they are the least interesting. They are small icons now, one per category, with the colour kept."},
			{Added, "An activity trail under Settings → Security: who changed a setting, who ran a prune, who signed in and who was refused. Silt records what changed on your host; this is the same question asked about Silt. It keeps what changed and never what it changed to, because settings hold an ingest token and notification targets and this is a list built to be read."},
			{Added, "SILT_AUDIT_RETENTION_DAYS, defaulting to two years. The trail is a row per administrative action rather than per observation, so it stays small, and its whole value is how far back it reaches."},
			{Fixed, "Every relative timestamp ran its own 30-second timer. The timeline renders a few hundred of them, so it ran a few hundred timers doing the same thing on their own phases, updating the page as a ripple. One shared clock now, stopped entirely when nothing is reading it."},
			{Fixed, "The header did not fit a phone. The wordmark, three labelled section links, a search box and the status control were wider than a 390px screen, and the overflow was invisible until opening a menu scrolled the whole page sideways to bring it into view. Section links are icons below that width and the wordmark gives way to the mark."},
			{Changed, "make check now runs the gofmt gate that CI runs as its own step. A local gate that does not match the remote one is worse than no local gate, because it is trusted."},
		},
	},
	{
		Version: "0.8.0",
		Date:    "2026-09-02",
		Summary: "A stopped container and an unhealthy one are not the same thing.",
		Entries: []Entry{
			{Fixed, "Silt showed a stopped container and an unhealthy one identically. On the service timeline they were two shades of the same red, indistinguishable at the eight pixels a mark actually gets; on the Projects screen everything that was not running was one count. So the question those screens exist to answer — is this thing down, or is it up and answering wrongly — was the one they could not answer."},
			{Added, "Exit codes. Silt now records why a container stopped, so \"exited (0)\" and \"exited (137)\" are different things on screen: one is someone stopping a stack, the other is something dying. A container killed by the kernel for memory is reported as OOM-killed rather than as exit 137, because an OOM kill and a docker kill share that code and only one of them is a memory limit to go and raise."},
			{Changed, "One state vocabulary everywhere. Running, starting, unhealthy, restarting, crashed, OOM-killed, stopped and paused each have their own colour and their own words, and a badge on the Projects screen now means the same thing as a dot on the service page and a row in the service table."},
			{Changed, "A container someone stopped on purpose is no longer flagged as a problem. It is grey, it does not put its stack in the attention list, and it does not colour anything red — colouring a deliberate stop red is how people learn to ignore red."},
			{Changed, "The Projects screen filters one failure mode at a time: unhealthy, crashed, restarting and stopped are separate chips rather than one \"not running\" number that named none of them."},
			{Changed, "The project's service table had a State column and a Health column of raw Docker strings, leaving \"running / unhealthy\" for the reader to interpret. It is one column with one verdict and the reason on hover."},
			{Added, "The service timeline has a legend, showing only the states that service has actually been in, in a fixed severity order so it does not reshuffle between services."},
			{Changed, "Upgrading writes one new snapshot per project. The exit code is part of the runtime fingerprint — without it a container that exited 0 and later exited 137 would be recorded as the same stop — so the first observation after the upgrade differs from the last one before it. Snapshots already recorded keep no exit code and are shown as plain \"exited\" rather than being guessed at."},
		},
	},
	{
		Version: "0.7.0",
		Date:    "2026-09-02",
		Summary: "The Projects screen says what needs you.",
		Entries: []Entry{
			{Changed, "Projects was a card per stack carrying its name and when it was last seen — on a host running forty of them, forty cards all saying \"2m ago\". Each card now says what is running, what is unhealthy, what has been restarting, and what was edited but never applied, and the default order puts the broken ones first."},
			{Added, "A summary strip above the grid, where every count is a filter. Seeing \"3 unhealthy\" and then having to hunt for which three was the thing this screen was worst at."},
			{Added, "Unapplied compose edits are a state, not just an event. Silt has always recorded config.drift when a file changed without the stack changing, but the event scrolls off the timeline while the file stays un-applied. The Projects screen answers whether it is still true, by comparing the files on disk against the ones in place at the last actual change."},
			{Added, "A \"send a test\" button for notification targets. A shoutrrr URL is wrong until something tries to send, and the only thing that tries to send is the change that mattered — so the first proof that notifications work used to be the outage they were configured for. Each target is reported separately, because the useful answer is which one is broken."},
			{Security, "Notification test failures are masked before they are shown. Providers quote the request URL back in their error text and a shoutrrr URL is a credential, so every fragment of the target is stripped from the message before it reaches the screen."},
			{Fixed, "Truncating a message for a notification cut on bytes rather than characters, which could split a multi-byte character into replacement glyphs."},
		},
	},
	{
		Version: "0.6.0",
		Date:    "2026-09-02",
		Summary: "Find anything, and a service page worth opening.",
		Entries: []Entry{
			{Added, "Search across every project, service, environment key, compose file and event. Press / from anywhere. On a host with forty-odd stacks, remembering which project a container belongs to was the slowest part of using Silt."},
			{Added, "Search matches environment variable names, never their values. A redacted value is not searchable by anyone, including whoever is signed in."},
			{Changed, "The service page was a list of observations; it is now a history. What the service is right now, every image it has run with how long it held, a link from each change straight to the diff that introduced it, restarts over time, and the environment keys that changed."},
			{Added, "A diff can leave Silt: Markdown from the structured view for pasting into an issue, and from the YAML view a real unified diff that patch(1) and git apply both accept — applying it turns the older compose document into the newer one. A change no longer has to travel as a screenshot."},
			{Fixed, "A locally built image has no registry digest, and its image ID was being labelled as one. The two are different claims and the page now says which it is showing."},
		},
	},
	{
		Version: "0.5.1",
		Date:    "2026-08-26",
		Summary: "OpenID Connect actually works with authentik.",
		Entries: []Entry{
			{Fixed, "An issuer with a trailing slash — which is how authentik publishes and prints its own — failed discovery, so the provider silently did not appear on the login screen. Silt was normalising the URL before handing it over, and the comparison is character for character."},
			{Fixed, "SILT_BASE_URL is no longer required to use a provider. The callback is derived from the request when nothing is configured, which behind a reverse proxy is the public name a browser actually used. Set it, or SILT_OIDC_REDIRECT_URL, to pin it."},
			{Fixed, "A provider that is configured but unreachable now says so on the login screen and under Settings → Security, with the reason. It used to just not appear, which sends you to the wrong place to debug it."},
			{Changed, "With a provider configured, a fresh install no longer forces you through local setup first. Sign in with the provider; add a password later under Settings → Security, or never."},
			{Security, "Claiming the built-in account anonymously is only possible when it is the only way in. Once a provider or a proxy could admit someone, an anonymous claim would be taking an account that bypasses them, so it requires a session."},
		},
	},
	{
		Version: "0.5.0",
		Date:    "2026-08-26",
		Summary: "A closed door on first boot, and a compose file short enough to read.",
		Entries: []Entry{
			{Security, "A fresh install is closed, not open. The built-in administrator exists from first boot, and the first thing Silt asks for is a password — until then every request is refused. The old default made the safe configuration the one you had to know to ask for."},
			{Added, "The built-in account can be renamed nothing and moved nowhere, but it can be managed: change its password, link it to a provider identity so signing in there reaches the same account, or turn it off once something else can let you in."},
			{Added, "SILT_PASSWORD_HASH now claims the account before Silt starts, which removes the first-run window entirely for anyone managing Silt declaratively. The UI then reports the password as the environment's and does not offer to change it."},
			{Security, "Changing the password revokes every other session, so doing it because you think one leaked also ends whatever leaked."},
			{Changed, "The compose file is short enough to read. Everything optional moved to .env, with a documented .env.example covering every setting and saying which ones need a restart."},
			{Changed, "Opening a project's compose files shows the whole file first. You are usually there to read the compose; the timeline already answers what changed, and links straight to the diff."},
			{Fixed, "The login screen was half-built: no way to start an OpenID Connect sign-in, and a password box on an install with no password. It now offers exactly what is configured, and says so when the answer is \"your reverse proxy should have done this\"."},
			{Fixed, "Reloading while signed out flashed the whole application before replacing it with the login form. Silt now waits until it knows whether it is locked."},
			{Added, "PNG renders of the mark beside the favicon, at 512 and 1024, light and dark, plus a padded tile."},
		},
	},
	{
		Version: "0.4.0",
		Date:    "2026-08-26",
		Summary: "Sign in with your identity provider, and a security pass over everything around it.",
		Entries: []Entry{
			{Added, "OpenID Connect login against any provider — authentik, Authelia, Keycloak, Pocket ID, Google. Authorization code flow with PKCE, a verified id_token, and optional group and user allowlists."},
			{Added, "Sessions are rows in Silt's database rather than signed cookies. They survive a restart, signing out revokes them server-side, and \"sign out everywhere\" ends all of them at once."},
			{Added, "A Security section on the settings screen: what is protecting this install, which provider, how many sessions exist, and the button to end them."},
			{Security, "Forward auth now believes the identity header only from addresses on SILT_TRUSTED_PROXIES. Without that list anything able to reach the port could claim to be anyone — which on a shared Docker network is every other container on it. An empty list still trusts any source, and now says so loudly at startup."},
			{Security, "/metrics requires authentication by default. It names every project on the host and counts its changes, which is not something to hand out because a scrape is easier without a token. SILT_METRICS_PUBLIC brings the old behaviour back."},
			{Security, "Unsafe requests from another origin are refused, so a page elsewhere cannot drive Silt through a signed-in browser."},
			{Security, "Content-Security-Policy, X-Frame-Options, X-Content-Type-Options, Referrer-Policy and Permissions-Policy on every response. Scripts may only come from Silt itself."},
			{Security, "Failed password attempts back off per client, after a few free tries so a typo costs nothing. bcrypt handles the offline attack; this handles the online one."},
			{Security, "The session cookie is marked Secure when the request arrived over TLS, directly or through a proxy that says so — rather than never, as before."},
			{Security, "The post-login destination is reduced to a same-origin path, so the login flow cannot be turned into an open redirect."},
		},
	},
	{
		Version: "0.3.0",
		Date:    "2026-08-26",
		Summary: "Compose files you can read, a timeline you can scan, and a layout you can choose.",
		Entries: []Entry{
			{Added, "Syntax highlighting for compose files, everywhere they are shown. Keys, strings, numbers, comments, anchors and ${VAR} references each get their own colour, and Silt's own redaction placeholder gets one too so a hidden value is visible as hidden."},
			{Added, "Comparing two snapshots as YAML now marks what actually changed: changed lines are tinted, the changed words inside them are highlighted, and the unchanged runs between them collapse. Side-by-side or unified, with the context adjustable."},
			{Added, "A second navigation layout. Sections can sit across the top or stacked in a left rail above the project list, the way authentik and Dockhand do it."},
			{Added, "Date and time preferences: 24-hour or 12-hour, day/month/year order, relative or absolute timestamps, and optional seconds. They live in your browser, so two people looking at the same Silt each get their own."},
			{Added, "A Ko-fi link in the version dialog, and a funding entry on the repository."},
			{Changed, "The project list scrolls on its own instead of making the page taller. A forty-project rail no longer decides how far the timeline scrolls."},
			{Changed, "The timeline reads at a glance: a fixed time column, a severity bar instead of a dot, day headings, hairline separators instead of a box per row, and bursts that expand in place. The density strip is taller by default, resizable, has a readable axis, and its legend doubles as a series filter."},
			{Changed, "Settings are organised into sections with their own navigation rather than one page taller than the window."},
			{Changed, "The strata-and-marker mark is now used throughout — header, favicon, sign-in, empty states."},
			{Fixed, "Choosing \"whole file\" when comparing YAML could lock the tab: the context window was walked one step at a time up to the maximum safe integer."},
		},
	},
	{
		Version: "0.2.0",
		Date:    "2026-08-26",
		Summary: "Settings you can edit, a timeline you can grab, and somewhere to put the version number.",
		Entries: []Entry{
			{Added, "Settings are editable from the UI. The environment stays the baseline; edits are stored as overrides on top of it, and every field shows whether it is coming from the environment or from here."},
			{Added, "Changed settings take effect without a restart: the snapshot interval, retention windows, keep-list, notification targets, base URL, log level and ingest token are all re-read by the running collector."},
			{Added, "The density strip is interactive — hover for the counts in a bucket, drag to zoom into a window, double-click to zoom back out."},
			{Added, "A version button in the header that opens the changelog."},
			{Changed, "Navigation is a top bar with Timeline, Projects and Settings, plus a project sidebar. Settings no longer sits at the bottom of a thirty-item scroll."},
			{Added, "A Projects page that lists every stack with its last change, for installs where the sidebar is a scroll rather than a glance."},
			{Changed, "The theme toggle is an animated sun/moon rather than the word \"Light\"."},
			{Security, "Notification targets are masked when read back. A shoutrrr URL carries the credential for the service it points at, so the settings screen shows the scheme and, where it is a real host, the host — never the token."},
		},
	},
	{
		Version: "0.1.0",
		Date:    "2026-08-25",
		Summary: "First working Silt: it watches, it records, it shows you what changed.",
		Entries: []Entry{
			{Added, "Docker event stream and interval reconcile, recording a snapshot of every Compose project whenever its configuration changes."},
			{Added, "Keep-list redaction: every environment value is a keyed digest unless its key is on the safe list, so the history answers \"when did this change?\" without ever holding the secret."},
			{Added, "Compose file capture with per-line diffs, and manual marking of lines to hide — click a line to redact it, click it again to reveal it."},
			{Added, "Timeline, project, service, diff and settings screens, live-updated over SSE."},
			{Added, "Retention with separate windows for changed snapshots, runtime-only snapshots and events, plus optional vacuuming."},
			{Added, "Notifications through shoutrrr, filtered by change kind and severity."},
			{Added, "Authentication: forward-auth from a reverse proxy, with a bcrypt password fallback."},
			{Added, "Multi-architecture images at ghcr.io/unmaykr-a/silt."},
		},
	},
}

// Current is the newest released version.
func Current() string {
	if len(Releases) == 0 {
		return "0.0.0"
	}
	return Releases[0].Version
}
