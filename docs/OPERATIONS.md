# Operations

## Remote deployment

For a non-loopback bind, configure `--tls-cert`, `--tls-key`, `--client-ca`, and `--admin-token-file`. The listener requires verified client certificates and refuses remote startup without an operator token. Session creation and agent registration require `X-Collab-Admin-Token`; the CLI reads it from a file or `COLLAB_ADMIN_TOKEN` and does not save it in project config. Full-ledger export also requires this operator token. Client TLS trust and mTLS identity are configured at `init` with `--ca`, `--client-cert`, and `--client-key`.

Sensitive reads (leases, events, queue, rendered reports, and verification) require an agent-signed request and are limited to that identity's session. A remote broker also needs access to the authoritative server-side Git checkout named during `init`; mTLS secures transport but does not provide that checkout. Protect the database, TLS keys, operator token, Git checkout, gate configuration, backup directory, and local agent keys with operating-system access controls. The broker assumes a single active broker process per SQLite ledger and export directory.

## Backup and export

`--export-dir` maintains `events.jsonl` by appending events and syncing writes. On restart, the broker verifies the database chain, checks that the backup exactly matches a prefix of the ledger, and appends any missing suffix. A backup mismatch fails startup rather than silently rewriting history.

`export --out FILE` provides a full-ledger snapshot that can be appended repeatedly only when the existing file exactly matches the verified server ledger prefix. These are tamper-evident exports, not immutable storage by themselves; copy them to separately controlled or offline storage for stronger backup guarantees.

## Request and receipt rules

Agent requests are timestamped and nonce-protected. The database stores event signatures and historical key versions so rotation and revocation do not invalidate verification of older events.

Receipts are derived from the server checkout (commit tree, changed paths, and patch hash). Gate results record outcome metadata and an output digest, not raw logs. Queue approval must come from an agent in the same session other than the submitter. The integration branch uses a compare-and-swap update-ref; a concurrent branch movement leaves the queue item retryable.

On Linux, the broker-mediated `write` endpoint uses directory file descriptors, no-follow opens, and atomic replacement to reject traversal and symlink escapes. It blocks unleased writes made through that endpoint, but cannot stop an agent with direct filesystem access from bypassing it. Use OS or container isolation, or a filesystem guard, if that enforcement is required.

## Container image

Every push to `main` publishes the agent terminal as a signed image:

```sh
docker run --rm -p 8787:8787 -v agentterm-data:/data ghcr.io/deontewattsv1/collaborate.asciidoc:main
```

The server listens on port 8787 inside the container and runs as a non-root user. Put TLS in front before exposing it.

## Observability

Set `OTEL_EXPORTER_OTLP_ENDPOINT` and the usual `OTEL_*` variables before `collaborate serve` to export OpenTelemetry traces and metrics. See [`OBSERVABILITY.md`](OBSERVABILITY.md).
