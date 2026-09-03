// First, and deliberately: it repairs a broken browser locale before anything
// reads one, and uPlot reads navigator.language at import time. See
// web/src/lib/localeguard.ts.
import "$lib/localeguard";
import { mount } from "svelte";
import "./app.css";
import App from "./App.svelte";
import { IS_DEMO, installDemoFetch } from "$lib/demo";

const target = document.getElementById("app");
if (!target) {
  throw new Error("missing #app mount point");
}

const start = () => mount(App, { target });

// The static demo answers /api from a captured file, and that has to be in
// place before the first component mounts: the screens fetch during setup, and
// a race there would show an error the demo can never recover from.
//
// The branch is a build-time constant, so a normal build keeps mounting
// synchronously and carries none of this.
if (IS_DEMO) {
  void installDemoFetch().then(start);
} else {
  start();
}
