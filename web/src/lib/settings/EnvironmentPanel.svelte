<script lang="ts">
  import Row from "./Row.svelte";
  import type { Settings } from "$lib/api/client";

  let { fixed }: { fixed: Settings["fixed"] } = $props();
</script>

<section>
  <h3 class="text-sm font-semibold">Environment only</h3>
  <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
    Three settings, and each for a reason this screen could not work around. The listen address and
    the database file are read once, by a socket and a file handle that cannot be swapped underneath
    a running process. The compose roots are an allowlist whose entries only mean anything alongside
    a matching read-only volume mount, so a path typed in here would name a directory this container
    cannot see. Change them in your compose file and recreate.
  </p>
  <dl class="mt-3 divide-y divide-border">
    <Row label="Listen address" value={fixed.listen_addr} envVar="SILT_LISTEN_ADDR" />
    <Row label="Database" value={fixed.db_path} envVar="SILT_DB_PATH" />
    <Row
      label="Compose roots"
      value={fixed.compose_roots.join(", ") || "none — file capture is off"}
      envVar="SILT_COMPOSE_ROOTS"
      hint="Each of these needs a matching read-only mount into the container."
    />
  </dl>
</section>
