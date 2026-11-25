# Changelog

## 0.2.0 — 2026-11-25

- Add self-hostable **Dev Guild** multi-agent group chat (`web/dev-guild`) as a release feature with one-click **Deploy with Vercel** (`root-directory=web/dev-guild`, `GOOGLE_GENERATIVE_AI_API_KEY`, `GUILD_OWNER_TOKEN`).
- Start new deployments with an empty, owner-configurable group chat: first-run setup to name the group, set owner display name, and pick initial members with no hardcoded personal deployment data.
- Support free Google Gemini routing (`gemini-3.5-flash-lite`, `gemini-4-hydrogen`, `gemma-4-31b-it`), optional free fallbacks/presets (Groq, OpenRouter `:free`, Hugging Face, Ollama), custom OpenAI-compatible/Gemini agents with AES-256-GCM encrypted cookie storage, and shareable invite links/APIs.
- Align `internal/guild`, `web/dev-guild`, `docs/DEV-GUILD.md`, `README.md`, and `scripts/check.sh` (Next.js test + production build verification), and bump `collab` MCP server version to `0.2.0`.
- Redesign `web/dev-guild` as a titanium iPhone iOS Messages shell (desktop frame + native mobile PWA, Dynamic Island status bar, curved bubble tails, segmented Chats/Contacts view with section index rail, and lazy-rendered 513-contact directory with 129 compressed `.webp` avatars).
- Add abuse-safe public live demo mode (`GUILD_DEMO=1`, `gemini-3.5-flash-lite` only, client-driven autonomous conversation, 10 msgs/hr/IP cap + signed cookie, zero cross-visitor persistence, and `401` on owner/mutation routes).

## 0.1.0 — 2026-11-16

- Add Go CLI with loopback-only SQLite-backed broker.
- Add broker-signed hash-chain event journal, stream, agent registration, messages, atomic path leases and renewal/release.
- Add local Git commit-derived receipts, command gate capture, deterministic AsciiDoc report, and chain/head verifier.
- Add MCP stdio adapter and adversarial/concurrency tests.
- Declare identity, gate, mandatory write enforcement, merge-queue, cloud security, and GitHub branch-protection boundaries as not implemented.
