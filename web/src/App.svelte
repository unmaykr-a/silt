<script lang="ts">
  type Health = "checking" | "ok" | "unreachable";

  // Probing /healthz from the page is the quickest proof that the UI and the
  // API are served from one origin by one binary, which is the M0 goal.
  let health = $state<Health>("checking");

  $effect(() => {
    const controller = new AbortController();
    fetch("/healthz", { signal: controller.signal })
      .then((r) => {
        health = r.ok ? "ok" : "unreachable";
      })
      .catch(() => {
        health = "unreachable";
      });
    return () => controller.abort();
  });

  const dotClass = $derived(
    health === "ok"
      ? "bg-emerald-400"
      : health === "checking"
        ? "bg-zinc-500"
        : "bg-red-400",
  );

  const label = $derived(
    health === "ok"
      ? "API healthy"
      : health === "checking"
        ? "checking API…"
        : "API unreachable",
  );
</script>

<main
  class="flex min-h-screen flex-col items-center justify-center bg-zinc-950 px-6 text-zinc-100"
>
  <h1 class="text-6xl font-semibold tracking-tight">Silt</h1>
  <p class="mt-3 text-lg text-zinc-400">
    What settled on your stack, and when.
  </p>

  <div
    class="mt-8 flex items-center gap-2 rounded-full border border-zinc-800 bg-zinc-900/60 px-4 py-2 text-sm text-zinc-400"
  >
    <span class="size-2 rounded-full {dotClass}" aria-hidden="true"></span>
    <span>{label}</span>
  </div>

  <p class="mt-10 max-w-md text-center text-sm text-zinc-600">
    No collectors are running yet. Project discovery arrives in M1.
  </p>
</main>
