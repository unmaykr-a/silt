import { defineConfig, type Plugin } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import tailwindcss from "@tailwindcss/vite";
import { writeFileSync } from "node:fs";
import { resolve } from "node:path";

const OUT_DIR = resolve(import.meta.dirname, "../internal/web/dist");

// The Go binary embeds ../internal/web/dist, and //go:embed fails to compile if
// that directory does not exist. The directory is kept in git by a .gitkeep
// placeholder, which emptyOutDir deletes on every build — leaving a spurious
// deletion in the working tree and breaking `go build` for anyone who cleaned
// before committing. Put it back after each build.
// See PROJECT.md Section 12.
function keepEmbedDir(): Plugin {
  return {
    name: "silt-keep-embed-dir",
    apply: "build",
    closeBundle() {
      writeFileSync(resolve(OUT_DIR, ".gitkeep"), "");
    },
  };
}

export default defineConfig({
  plugins: [tailwindcss(), svelte(), keepEmbedDir()],
  build: {
    outDir: OUT_DIR,
    // outDir sits outside the Vite root, so this must be explicit.
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    // `npm run dev` proxies to a locally running `silt` so the UI and API
    // share an origin, exactly as they do in the built binary.
    proxy: {
      "/api": "http://localhost:8375",
      "/healthz": "http://localhost:8375",
    },
  },
});
