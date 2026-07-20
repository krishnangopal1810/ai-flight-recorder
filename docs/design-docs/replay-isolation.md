# Replay isolation

Controlled replay requires Docker or Podman. The application runs non-root with dropped capabilities, a read-only root filesystem, tmpfs scratch space, resource limits, no Docker socket, and no host-writable mounts. It joins only a per-job internal network.

The Flight gateway is the sole route for candidate OTLP, recorded boundary substitution, and explicitly declared model or sandbox egress. It joins the operator-selected OCI egress network only when the replay plan contains allowlisted live hosts; the application never joins that network. Unknown destinations and uninstrumented direct egress are blocked. Failure to establish any isolation control blocks preflight.
