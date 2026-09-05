<script lang="ts">
  import Row from "./Row.svelte";
  import { bytes } from "$lib/format";
  import type { Settings } from "$lib/api/client";

  let { fixed }: { fixed: Settings["fixed"] } = $props();
</script>

<section>
  <h3 class="text-sm font-semibold">Environment only</h3>
  <p class="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">
    These cannot be changed here. Some need a restart to take effect; the rest are the boundary
    protecting this screen — a UI that could widen which files Silt reads, or turn off the login in
    front of it, would be a way in rather than a setting.
  </p>
  <dl class="mt-3 divide-y divide-border">
    <Row label="Host name" value={fixed.host_name} envVar="SILT_HOST_NAME" />
    <Row label="Docker endpoint" value={fixed.docker_host} envVar="SILT_DOCKER_HOST" />
    <Row label="Database" value={fixed.db_path} envVar="SILT_DB_PATH" />
    <Row label="Listen address" value={fixed.listen_addr} envVar="SILT_LISTEN_ADDR" />
    <Row
      label="Authentication"
      value={fixed.auth_mode}
      envVar="SILT_TRUST_PROXY_AUTH / SILT_PASSWORD_HASH"
    />
    <Row
      label="Compose roots"
      value={fixed.compose_roots.join(", ") || "none — file capture is off"}
      envVar="SILT_COMPOSE_ROOTS"
    />
    <Row
      label="Max compose file"
      value={bytes(fixed.max_compose_file_bytes)}
      envVar="SILT_MAX_COMPOSE_FILE_BYTES"
    />
  </dl>
</section>
