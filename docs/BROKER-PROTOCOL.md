# Broker event ledger

The `collaborate` broker records every coordination action in an append-only ledger. The ledger is the shared history of a session: claims, messages, writes, receipts, reviews, and merges. It is not a Git log, and it is not a substitute for CI.

## Event shape

| Field | Meaning |
|---|---|
| `seq` | Monotonic sequence assigned by the broker. |
| `session` | Session the event belongs to. |
| `agent` | Agent that caused the event. |
| `type` | Event type (below). |
| `payload` | Type-specific JSON. |
| `ts` | Broker clock, Unix milliseconds. |
| `prev_hash` | Hash of the previous event in the session. |
| `request_digest` | Digest of the signed request that produced the event. |
| `signature` | Agent signature over the request digest. |
| `key_version` | Agent key version used to sign. |
| `hash` | SHA-256 of the event pre-image. |

The exact pre-image is defined in [`internal/store/store.go`](../internal/store/store.go). It covers the sequence, session, agent, type, payload, timestamp, previous hash, request digest, signature, and key version.

## Event types

| Type | Recorded when |
|---|---|
| `CLAIM` | An agent takes an expiring lease on paths. |
| `RELEASE` | An agent releases paths it owns. |
| `RENEW` | An agent extends a live lease. |
| `SAY` | An agent posts a coordination message. |
| `WRITE` | The broker writes a file inside an agent's worktree. |
| `WORKTREE` | The broker creates an agent worktree and branch. |
| `RECEIPT` | A commit receipt is derived from the server-side checkout. |
| `SUBMIT` | A verified commit enters the review queue. |
| `REVIEW` | An independent agent approves or requests changes. |
| `MERGE` | The queue item is integrated into the session trunk. |
| `MERGE_RETRY` | An integration attempt lost a compare-and-swap race and can be retried. |
| `KEY_ROTATE` | An agent rotates its Ed25519 key. |
| `KEY_REVOKE` | An agent revokes an older key version. |

## Integrity

The broker verifies the full chain before serving sensitive reads. `collaborate verify` walks the ledger and checks each link and signature. An export (`collaborate export`) writes a JSONL snapshot that can be appended only when it matches a verified prefix of the server ledger.

The ledger is tamper-evident, not tamper-proof. Whoever controls the database can delete the file or withhold events. Pin the head hash somewhere independent if the history matters beyond the host.

## Trust boundary

The ledger proves what the broker recorded, and that an agent signed the request that produced each event. It does not prove that an agent's self-reported gate output is true, that a commit exists on a remote, or that a model identity is real. Use independent CI, protected branches, and per-agent credentials for those guarantees.
