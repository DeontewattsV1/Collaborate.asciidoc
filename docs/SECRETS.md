# Secure secret drop

People in a group chat can hand API keys, tokens and other credentials to the
room, optionally pre-scoped and granted to specific agents, with a name and a
link to where the secret came from. The chat stores it encrypted, can verify
it with the provider, and can push it to deployment environments, without the
raw value ever landing in the transcript.

**Status: off on the live Fly app.** The vault needs Postgres
(`GUILD_STORE=postgres` + `DATABASE_URL`) and `SECRETS_MASTER_KEY`. Neither is
set on `collaborate-asciidoc.fly.dev` today, so the vault routes answer `503`
and store nothing. Chat redaction is always on.

## What the user sees

- **Paste a key in the composer.** The browser detector catches it before
  sending. Nothing is sent; the owner gets the **Add secret** sheet prefilled
  with the value. Anyone else gets a "that looked like a secret" warning.
- **The 🔑 button** in the composer opens the same glass sheet: value
  (password field), env-var style name, provider (auto-detected), where it's
  from (name and https link), scopes, environments (dev / staging / prod),
  allowed agents (none by default), agent access window, "let agents read the
  raw value" (off by default) and an optional expiry. It honours reduced
  transparency and increased contrast.
- **The transcript** shows a redacted secret card: name, provider, `…last4`,
  source link, scopes, environments, allowed agents and status
  (`unverified`, `verified`, `invalid`, `revoked`, `expired`).
- If a key still reaches the server (API client, old browser, an agent echoing
  one), the server redacts it to `[secret removed: Provider …abcd]`. In a
  durable room, an **owner's** detected key is sealed into the vault and shown
  as a card; anyone else's is dropped.

## Threat model

| Threat | Control |
|---|---|
| Key ends up in chat history, search, push, exports | Client + server detector; every write path (`/api/guild/stream`, `/api/guild/v1/threads/:id/messages`, invite messages, durable `sendMessage`, and `appendLine` for agent output) redacts before storage |
| Key ends up in an agent prompt or the VM bridge | Routes redact `text` before building prompts or forwarding |
| Key ends up in logs | `instrumentation.js` wraps `console.*` with the same redactor; route errors are generic and never echo bodies |
| Database dump or backup leak | Envelope encryption (below). Rows hold ciphertext, nonce, wrapped data key, an HMAC fingerprint, `last4` and metadata only |
| Row swap / ciphertext replay | AES-GCM additional data binds room, secret id and version |
| Agent overreach | Deny by default. Access needs an explicit grant (agent id, scopes, expiry, use budget). Agents get single-use 5-minute handles, not values |
| Raw value exfiltration by an agent | Values are revealed only for grants the owner marked `reveal: true`, and only to the agent the handle was issued to |
| Non-owner abuse | Only room owners add, rotate, revoke, grant, verify, sync or read the audit log; members/viewers see cards; outsiders get the same 403 as a missing room |
| CSRF / cross-site calls | Cookie-auth writes require a same-origin `Origin`; `Sec-Fetch-Site: cross-site` is refused; durable rooms use bearer tokens |
| Brute force / scraping | Per-client rate limits on every secret route; 32 KB body limit; 16 KB value limit |
| Tampering with history | Audit log is hash-chained and Postgres refuses `UPDATE`/`DELETE` on it |
| Leaked key after the fact | Rotate (kills outstanding handles) or revoke (crypto-shreds ciphertext and wrapped key, revokes grants) |
| Accidental production push | Sync is a dry-run plan first; apply needs a single-use, 10-minute owner approval bound to that plan **and** `SECRETS_SYNC_APPLY=on` |
| Verifier leaking the key elsewhere | Verification is off unless `SECRETS_VERIFY=on`, uses one read-only "who am I" call per provider, refuses redirects and stores only the status |

Out of scope: a compromised server process or host (it holds the master key),
and a malicious owner. Strings handed to provider SDKs cannot be zeroized in
JavaScript; buffers are zeroized where the code controls them.

## Cryptography

- `SECRETS_MASTER_KEY`: 32 random bytes, base64 or hex. Missing, short or
  low-entropy keys fail closed (`503`, nothing stored).
- HKDF-SHA256 derives three keys from it: a key-encryption key, a fingerprint
  HMAC key and a handle HMAC key.
- Each secret gets a fresh 256-bit data key. The value is sealed with
  AES-256-GCM under the data key; the data key is wrapped with AES-256-GCM
  under the key-encryption key. Plaintext and data-key buffers are zeroized.
- Handles are 256-bit random strings; only their HMAC is stored.
- Compares use constant-time equality.

## Data model (migration `003_secrets.sql`)

- `guild_secrets` – metadata + envelope (`ciphertext`, `nonce`, `wrapped_key`,
  `fingerprint`, `last4`, `version`, status, expiry, timestamps).
- `guild_secret_grants` – agent id, scopes, `reveal`, `expires_at`, `max_uses`, `uses`.
- `guild_secret_handles` – HMAC of each short-lived handle.
- `guild_secret_sync_approvals` – single-use owner approvals bound to a plan digest.
- `guild_secret_audit` – append-only, hash-chained, value-free.

Apply it with `node scripts/migrate-guild-db.mjs` (same runner as 001/002).

## API

Durable rooms (bearer identity token, see `docs/DEV-GUILD.md`):

| Route | Who | What |
|---|---|---|
| `GET /api/rooms/:room/secrets` | any member | Redacted cards (grants only for owners) |
| `POST /api/rooms/:room/secrets` | owner | Add `{name, value, provider?, sourceName?, sourceUrl?, scopes?, environments?, expiresAt?, allowedAgents?: [{agentId, scopes?, ttlSeconds?, maxUses?, reveal?}]}` |
| `POST /api/rooms/:room/secrets/:id` | owner | `{action: "rotate", value}` · `"revoke"` · `"verify"` · `"grant"` · `"revoke-grant", grantId` · `"plan-sync", target, environment` · `"apply-sync", approvalId` |
| `DELETE /api/rooms/:room/secrets/:id` | owner | Revoke and crypto-shred |
| `POST /api/rooms/:room/secrets/:id/handle` | granted agent identity (room member) | `{scope?}` → single-use handle, 5 minutes |
| `POST /api/rooms/:room/secrets/redeem` | same agent | `{handle}` → value **only** for `reveal` grants |
| `GET /api/rooms/:room/secrets/audit` | owner | Audit log |

The owner-cookie chat uses `/api/guild/secrets`, `/api/guild/secrets/:id` and
`/api/guild/secrets/audit` with the same bodies, scoped to the `guild` room.

Server-side agent tools should call `withSecretValue(db, handle, fn)` from
`lib/secrets/vault.js`: it spends the handle, decrypts, runs `fn(buffer)` and
zeroizes the buffer afterwards.

## Verification

`SECRETS_VERIFY=on` enables one read-only call per provider: OpenAI and Groq
`/models`, Anthropic `/v1/models`, Gemini `/v1beta/models`, GitHub `/user`,
Cloudflare `/user/tokens/verify`, Stripe `/v1/balance`, Netlify `/user`,
Slack `auth.test`, Fly `viewer`. AWS and generic secrets stay `unverified`.
401/403 → `invalid`; network errors and 5xx → `unverified`. These calls are
free, but they do leave the server, so the flag is off by default.

## Sync targets

| Target | Config | Apply |
|---|---|---|
| `fly` | `SECRETS_SYNC_FLY_APP[_DEV/_STAGING/_PROD]`, `SECRETS_SYNC_FLY_TOKEN` | GraphQL `setSecrets` (restarts machines) |
| `github` | `SECRETS_SYNC_GITHUB_REPO[_ENV]` | Plan-only: prints `gh secret set NAME --repo … --env …` (value via stdin). Automated apply needs a libsodium sealed box and is deferred |
| `cloudflare` | `SECRETS_SYNC_CF_ACCOUNT`, `SECRETS_SYNC_CF_SCRIPT[_ENV]`, `SECRETS_SYNC_CF_TOKEN` | Worker secret `PUT` |
| `netlify` | `SECRETS_SYNC_NETLIFY_ACCOUNT`, `SECRETS_SYNC_NETLIFY_SITE[_ENV]`, `SECRETS_SYNC_NETLIFY_TOKEN` | Env var with `is_secret` (dev→`dev`, staging→`deploy-preview`, prod→`production`) |
| `dotenv` | `SECRETS_SYNC_DOTENV_DIR` | Writes `.env.<environment>` with mode `0600` on the server's disk |

A secret syncs only to environments it was scoped to. Flow: `plan-sync`
(dry run, value-free, returns `approvalId`) → owner reviews → `apply-sync`
with that `approvalId`, while `SECRETS_SYNC_APPLY=on`. Sync tokens are
deployment secrets themselves; set them on the host, never in chat.

## Operations

1. Generate a key locally and keep it out of git and chat:
   `node -e "console.log(require('crypto').randomBytes(32).toString('base64'))"`.
2. Set it as a host secret (for Fly: `fly secrets set SECRETS_MASTER_KEY=...`;
   note that this restarts the machine). Back it up in a password manager:
   **losing it makes every stored secret unrecoverable.**
3. Provide Postgres (`GUILD_STORE=postgres`, `DATABASE_URL`) and run the migration.
4. Key rotation: a re-wrap tool is not built yet. Today, rotate by revoking
   and re-adding secrets under the new key.
5. Review `GET …/secrets/audit` after any incident; check the chain with
   `verifyAuditChain(db, roomId)`.

### CSP note

The secret routes send `Cache-Control: no-store`, `Referrer-Policy:
no-referrer` and `X-Content-Type-Options: nosniff`. The app does not yet send
a Content-Security-Policy. Before enabling the vault on a public host, add a
CSP of at least `default-src 'self'; frame-ancestors 'none'; form-action
'self'` (plus whatever Next.js inline scripts need via nonces) so injected
script cannot read the sheet.

## Tests

`npm test` in `web/dev-guild` runs `lib/secrets/*.test.js`: detector and
redaction, crypto roundtrip/tamper/fail-closed, guards (origin, rate limit,
size, log redaction), sync plans and adapters with a fake fetch, and (with
`GUILD_TEST_DATABASE_URL`) the full vault contract: deny-by-default, grants,
expiry, use budgets, reveal, rotation, revocation, chat capture, and the
append-only audit chain. Tests use fake values and make no network calls.
