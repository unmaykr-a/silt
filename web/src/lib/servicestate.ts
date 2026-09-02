/**
 * One vocabulary for what a container is doing, used everywhere it is shown.
 *
 * Before this, each screen decided for itself. The service page painted an
 * unhealthy container `bg-red-500` and a stopped one `bg-red-500/70` — two
 * shades of the same red, indistinguishable at the eight pixels a timeline
 * mark actually gets. The projects screen folded everything that was not
 * running into one "stopped" count. So the one question these screens exist to
 * answer — is this thing down, or is it up and answering wrongly — was the one
 * they could not answer.
 *
 * They are genuinely different problems. An unhealthy container is running:
 * the process is alive, the port is open, and the healthcheck says the thing
 * behind it is wrong. A stopped container is not there at all. A restarting
 * one is in a loop. And a container someone stopped on purpose is not a
 * problem, which is why `exited 0` is deliberately not an alarm colour.
 */

/** The distinct things a container can be, in severity order. */
export type StateKey =
  | "unhealthy"
  | "oom"
  | "crashed"
  | "restarting"
  | "stopped"
  | "paused"
  | "starting"
  | "running"
  | "unknown";

export type ServiceState = {
  key: StateKey;
  /** Short label for a badge or a table cell. */
  label: string;
  /** A sentence for a title attribute: what this actually means. */
  detail: string;
  /** Tailwind background, for a dot or a timeline mark. */
  dot: string;
  /** Tailwind text colour, for a label. */
  text: string;
  /**
   * Whether this is a state to go and look at. `exited 0` is not: somebody
   * stopped it, and colouring that red trains people to ignore red.
   */
  attention: boolean;
};

const states: Record<StateKey, Omit<ServiceState, "key" | "label" | "detail">> = {
  // Running but failing its healthcheck. Red, because the service is lying
  // about being available.
  unhealthy: { dot: "bg-red-500", text: "text-red-600 dark:text-red-400", attention: true },
  // Killed for memory. Its own colour: this is the one with a specific fix.
  oom: { dot: "bg-fuchsia-500", text: "text-fuchsia-600 dark:text-fuchsia-400", attention: true },
  // Stopped with a non-zero code. Nobody asked for this.
  crashed: { dot: "bg-orange-500", text: "text-orange-600 dark:text-orange-400", attention: true },
  // In a loop.
  restarting: { dot: "bg-amber-500", text: "text-amber-600 dark:text-amber-400", attention: true },
  // Stopped, cleanly. Grey on purpose — this is a state, not a fault.
  stopped: { dot: "bg-zinc-400 dark:bg-zinc-500", text: "text-muted-foreground", attention: false },
  paused: { dot: "bg-violet-400", text: "text-violet-600 dark:text-violet-400", attention: false },
  starting: { dot: "bg-sky-400", text: "text-sky-600 dark:text-sky-400", attention: false },
  running: { dot: "bg-emerald-500", text: "text-emerald-600 dark:text-emerald-400", attention: false },
  unknown: { dot: "bg-zinc-300 dark:bg-zinc-700", text: "text-muted-foreground", attention: false },
};

export type StateInput = {
  state?: string;
  health?: string;
  exit_code?: number | null;
  oom_killed?: boolean;
};

/**
 * Classify one observation.
 *
 * Order matters and is severity, not Docker's. A running container that is
 * unhealthy is reported as unhealthy rather than as running, because "running"
 * is the answer that would send someone looking somewhere else.
 */
export function serviceState(o: StateInput): ServiceState {
  const state = (o.state ?? "").toLowerCase();
  const health = (o.health ?? "").toLowerCase();
  const code = o.exit_code ?? null;

  const make = (key: StateKey, label: string, detail: string): ServiceState => ({
    key,
    label,
    detail,
    ...states[key],
  });

  switch (state) {
    case "running":
      if (health === "unhealthy") {
        return make("unhealthy", "unhealthy", "Running, but its healthcheck is failing.");
      }
      if (health === "starting") {
        return make("starting", "starting", "Running; its healthcheck has not passed yet.");
      }
      if (health === "healthy") {
        return make("running", "healthy", "Running and passing its healthcheck.");
      }
      // No healthcheck at all. Not the same as healthy, and saying "healthy"
      // about a container nobody is checking would be inventing a fact.
      return make("running", "running", "Running. This container has no healthcheck.");

    case "restarting":
      return make("restarting", "restarting", "In a restart loop, or coming back up.");

    case "paused":
      return make("paused", "paused", "Suspended with docker pause.");

    case "created":
      return make("starting", "created", "Created but never started.");

    case "exited":
    case "dead": {
      const dead = state === "dead";
      if (o.oom_killed) {
        return make("oom", "OOM-killed", "The kernel killed it for exceeding its memory limit.");
      }
      if (code === null || code === undefined) {
        return make(
          "stopped",
          dead ? "dead" : "exited",
          dead ? "Dead: Docker could not remove it." : "Not running.",
        );
      }
      if (code === 0) {
        return make("stopped", "exited (0)", "Stopped cleanly. Nothing went wrong.");
      }
      return make("crashed", `exited (${code})`, `Stopped with exit code ${code}. Nobody asked it to.`);
    }

    case "":
      return make("unknown", "unknown", "Silt has no state for this container.");

    default:
      return make("unknown", state, `Docker reports this container as ${state}.`);
  }
}

/** The legend, in the order the screens show it. */
export const stateLegend: { key: StateKey; label: string }[] = [
  { key: "running", label: "running" },
  { key: "starting", label: "starting" },
  { key: "unhealthy", label: "unhealthy" },
  { key: "restarting", label: "restarting" },
  { key: "crashed", label: "crashed" },
  { key: "oom", label: "OOM-killed" },
  { key: "stopped", label: "stopped" },
  { key: "paused", label: "paused" },
];

export function dotFor(key: StateKey): string {
  return states[key].dot;
}
