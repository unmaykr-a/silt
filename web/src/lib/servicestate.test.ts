import { describe, it, expect } from "vitest";
import { serviceState } from "./servicestate";

describe("serviceState", () => {
  // The whole reason this module exists: these three used to be one colour
  // and one count, so the screens could not say which you were looking at.
  it("keeps unhealthy, stopped and restarting apart", () => {
    const unhealthy = serviceState({ state: "running", health: "unhealthy" });
    const stopped = serviceState({ state: "exited", exit_code: 0 });
    const restarting = serviceState({ state: "restarting" });

    const keys = [unhealthy.key, stopped.key, restarting.key];
    expect(new Set(keys).size).toBe(3);
    const dots = [unhealthy.dot, stopped.dot, restarting.dot];
    expect(new Set(dots).size).toBe(3);
  });

  it("reports an unhealthy container as unhealthy, not as running", () => {
    // "running" is the answer that sends someone looking somewhere else.
    expect(serviceState({ state: "running", health: "unhealthy" }).label).toBe("unhealthy");
    expect(serviceState({ state: "running", health: "unhealthy" }).attention).toBe(true);
  });

  it("does not call a clean stop a problem", () => {
    const s = serviceState({ state: "exited", exit_code: 0 });
    expect(s.attention).toBe(false);
    // Colouring a deliberate stop red trains people to ignore red.
    expect(s.dot).not.toContain("red");
  });

  it("calls a non-zero exit a crash and says the code", () => {
    const s = serviceState({ state: "exited", exit_code: 137 });
    expect(s.key).toBe("crashed");
    expect(s.label).toContain("137");
    expect(s.attention).toBe(true);
  });

  it("separates an OOM kill from a plain kill", () => {
    // Both are 137. Only one of them is a memory limit to go and raise.
    const oom = serviceState({ state: "exited", exit_code: 137, oom_killed: true });
    const killed = serviceState({ state: "exited", exit_code: 137, oom_killed: false });
    expect(oom.key).toBe("oom");
    expect(killed.key).toBe("crashed");
    expect(oom.dot).not.toBe(killed.dot);
  });

  it("does not claim a container with no healthcheck is healthy", () => {
    const s = serviceState({ state: "running", health: "" });
    expect(s.label).toBe("running");
    expect(s.detail).toContain("no healthcheck");
    expect(s.attention).toBe(false);
  });

  it("treats a starting healthcheck as starting, not as failing", () => {
    const s = serviceState({ state: "running", health: "starting" });
    expect(s.key).toBe("starting");
    expect(s.attention).toBe(false);
  });

  it("does not invent an exit code that was never recorded", () => {
    // A snapshot taken before Silt captured exit codes has none. Rendering
    // that as "exited (0)" would claim a clean stop that was never observed.
    const s = serviceState({ state: "exited" });
    expect(s.label).toBe("exited");
    expect(s.key).toBe("stopped");
  });

  it("passes an unrecognised state through rather than hiding it", () => {
    const s = serviceState({ state: "removing" });
    expect(s.label).toBe("removing");
    expect(s.key).toBe("unknown");
  });

  it("every state that wants attention has a distinct colour", () => {
    const attention = [
      serviceState({ state: "running", health: "unhealthy" }),
      serviceState({ state: "exited", exit_code: 1 }),
      serviceState({ state: "exited", exit_code: 137, oom_killed: true }),
      serviceState({ state: "restarting" }),
    ];
    expect(attention.every((s) => s.attention)).toBe(true);
    expect(new Set(attention.map((s) => s.dot)).size).toBe(attention.length);
  });
});
