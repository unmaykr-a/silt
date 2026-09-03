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
  // Silt serves itself from the root, so "/" is right for every real install.
  // The GitHub Pages demo lives under a project path, and `make demo-site`
  // passes that in. Everything that builds a URL reads it back through
  // import.meta.env.BASE_URL — see web/src/lib/router.svelte.ts.
  base: process.env.SILT_BASE_PATH || "/",
  plugins: [tailwindcss(), svelte(), keepEmbedDir()],
  resolve: {
    // $lib is the SvelteKit convention that shadcn-svelte components import
    // through; this project is plain Vite, so the alias is declared here and
    // mirrored in tsconfig.json.
    alias: { $lib: resolve(import.meta.dirname, "src/lib") },
  },
  build: {
    outDir: process.env.SILT_WEB_OUT ? resolve(process.env.SILT_WEB_OUT) : OUT_DIR,
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
