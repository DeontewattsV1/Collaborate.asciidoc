# Security and release hold — v0.1

**Security status: NOT production-ready.** This is a demonstrator for live inter-agent coordination and event-integrity checks.

## Verified in local tests

- Concurrent lease claims serialize through SQLite transactions.
- The broker checks ownership for release and renewal, and live lease coverage for receipts.
- An unrecognized bearer token receives HTTP 401; an `Origin` request header is refused to reduce simple cross-origin browser access.
- Ledger verification detects edits to existing event rows with the originally pinned Ed25519 public key.
- An externally pinned head detects a truncated suffix.

## Not guaranteed

- **Agent authentication:** all clients share one token; a client can claim any registered actor name. Model labels are self-declared.
- **Mandatory filesystem enforcement:** the broker cannot intercept direct editors, filesystem syscalls, or unauthorized Git pushes. Worktree separation is cooperative until a separately managed sandbox or protected merge gate is deployed.
- **Commit validation at service boundary:** HTTP receipt payloads need syntactically valid SHA strings and held leases, not actual remote Git objects. The terminal CLI checks local Git, but this can be bypassed by using HTTP directly.
- **Test credibility:** local `gate` invokes a process and records the exit code/output hash, but the broker cannot independently attest to it. A malicious agent can send fabricated gate records. Use independently managed CI and signed build provenance.
- **Complete append-only persistence:** a database owner can truncate, replace, or destroy the database; pinning hashes externally is necessary. Key compromise defeats broker signatures.
- **Cloud safety:** loopback HTTP only. Shared tokens over a tunnel or an incorrectly configured proxy can leak. Use end-to-end TLS, per-agent identity, secure secret handling, and private networking before distributed deployment.
- **Availability:** no leader election, multi-broker SSE fanout, encrypted SQLite content, durable replicated log, formal path-locking, quotas, or resource sandboxing.

## Production gates

1. Per-agent short-lived mTLS or signed request authentication, scoped to an agent ID and project; human-reviewed model/provider identity bindings.
2. Server-side Git object fetch and complete changed-file check, immutable worktree snapshot, symlink/path protection, versioned lease fencing tokens and compare-and-swap merge queue.
3. Mandatory protected branch checks and independent isolated CI runner attestations; never accept self-reported PASS as proof.
4. Externally anchored/replicated ledger heads, key rotation protocol, log retention and privacy policy, secure back-ups and disaster recovery.
5. Threat-model review and external adversarial tests before allowing autonomous edits to sensitive repositories.

The system is held at `PROTOTYPE / NOT DEPLOYED` until these gates are independently assessed. No automatic merges or external repository release permissions are implemented.
