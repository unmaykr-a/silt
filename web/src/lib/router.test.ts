import { describe, it, expect } from "vitest";
import { parseRoute, joinBase, stripBase } from "./routing";

describe("route parsing", () => {
  it("reads every screen", () => {
    expect(parseRoute("/", "")).toEqual({ name: "timeline" });
    expect(parseRoute("/projects", "")).toEqual({ name: "projects" });
    expect(parseRoute("/settings", "")).toEqual({ name: "settings" });
    expect(parseRoute("/projects/7", "")).toEqual({ name: "project", projectId: 7 });
    expect(parseRoute("/projects/7/services/radarr", "")).toEqual({
      name: "service",
      projectId: 7,
      service: "radarr",
    });
    expect(parseRoute("/search", "?q=radarr")).toEqual({ name: "search", query: "radarr" });
  });

  it("decodes a service name with a slash or a space", () => {
    expect(parseRoute("/projects/1/services/my%20app", "")).toEqual({
      name: "service",
      projectId: 1,
      service: "my app",
    });
  });

  it("refuses a project id that is not a number", () => {
    expect(parseRoute("/projects/abc", "").name).toBe("notfound");
  });

  it("falls through to not found", () => {
    expect(parseRoute("/nope", "").name).toBe("notfound");
    expect(parseRoute("/projects/1/nonsense", "").name).toBe("notfound");
  });
});

describe("base path", () => {
  // Silt serves itself from the root, so the base is "/" in every real
  // install. The demo on GitHub Pages lives under a project path, and without
  // this every route there parses as not found: the base is read as the first
  // segment.
  it("is a no-op at the root", () => {
    expect(stripBase("/projects/1", "")).toBe("/projects/1");
    expect(joinBase("/projects/1", "")).toBe("/projects/1");
  });

  it("strips the mount point before parsing", () => {
    expect(stripBase("/silt/projects/1", "/silt")).toBe("/projects/1");
    expect(stripBase("/silt", "/silt")).toBe("/");
    expect(stripBase("/silt/", "/silt")).toBe("/");
  });

  it("leaves a path that is not under the mount point alone", () => {
    expect(stripBase("/other/projects", "/silt")).toBe("/other/projects");
    // A prefix match is not enough: /siltation is not under /silt.
    expect(stripBase("/siltation/x", "/silt")).toBe("/siltation/x");
  });

  it("prefixes the mount point when building a URL", () => {
    expect(joinBase("/projects/1", "/silt")).toBe("/silt/projects/1");
    expect(joinBase("projects/1", "/silt")).toBe("/silt/projects/1");
  });

  it("does not double-prefix", () => {
    expect(joinBase("/silt/projects/1", "/silt")).toBe("/silt/projects/1");
  });

  it("leaves an absolute URL alone", () => {
    expect(joinBase("https://ko-fi.com/unmaykr", "/silt")).toBe("https://ko-fi.com/unmaykr");
  });

  it("round-trips", () => {
    for (const path of ["/", "/projects", "/projects/1/services/radarr", "/settings"]) {
      expect(stripBase(joinBase(path, "/silt"), "/silt")).toBe(path);
    }
  });
});
