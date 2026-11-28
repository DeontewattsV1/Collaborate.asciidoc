# Brand system

This page connects two lines of work. The brand direction was drafted in ChatGPT. The product is built in this repository. Where they disagree, this page records the decision, and the repository stays the source of truth for counts, features, and security claims.

Status: **proposed v1.0, awaiting the maintainer's approval.** Names, taglines, the crest, and the guild houses are creative proposals. No trademark clearance has been done.

## Sources

| Source | What it contributes | Status |
|---|---|---|
| [Repository](https://github.com/DeontewattsV1/Collaborate.asciidoc) | The product: the room, the broker, the shared terminal, the GitHub check. Its README holds the canonical one-line description. | Source of truth |
| [GitHub Pages tour](https://deontewattsv1.github.io/Collaborate.asciidoc/) (`site/index.html`) | The public landing page, in iPhone style | Live |
| [Live app](https://collaborate-asciidoc.fly.dev) (`web/dev-guild`, Fly.io) | The hosted group chat and `/terminal` | Live |
| [ChatGPT: "Build Brand Typography"](https://chatgpt.com/share/6b09b014-c7ac-83e8-a337-5d90966a69bf) | Brand architecture, taglines, type system, palette, 18 guild houses, character bible, voice rules, and later a Liquid-Glass chat redesign and a "v5" infrastructure plan | Proposal. Its download packages never reached this repository. |
| [ChatGPT: "Free AI Coding Agents"](https://chatgpt.com/share/6b09b03d-a304-83e8-9ca4-053bb8fa192e) | The roster: 75 coding agents plus API-key developer tools by category | Already reflected in [`GUILD-DIRECTORY.md`](GUILD-DIRECTORY.md) |

## Names

There are three names, and each has one job.

| Name | Job | Where it appears |
|---|---|---|
| **Collaborate** | The product, and the name of the group chat | App title, home-screen icon, page titles, conversation |
| **The Dev Guild** | The character universe and community inside Collaborate | Character cards, house crests, posters, social posts, the roster |
| **Collaborate.asciidoc** | The repository and technical project | GitHub, CLI docs, citations |

Parent organization: **GoodShyt Group Inc.**

The ChatGPT draft first named the product experience "The Dev Guild" and later asked for the group chat to be called "Collaborate Asciidoc". Splitting the jobs settles that conflict. People open **Collaborate** and meet **the Dev Guild** there. The `.asciidoc` suffix stays in technical contexts, because non-developers do not know what it means.

## Positioning

- **What it is (from the README):** A shared room where humans and AI agents work on the same codebase.
- **Primary tagline:** Different agents. One guild.
- **Secondary tagline:** Build together. Ship together.
- **Promise:** You can see who is coordinating, who contributed what, and who handed off to whom.
- **Audience:** Developers, AI engineers, researchers, open-source maintainers, and technical teams that run more than one agent.

Lead with the README line, which says what the product does, and close with the tagline, which says how it feels. Do not lead with the tagline alone.

### Why it is different

Other agent tools offer either a personality or a guarantee. Collaborate offers both, and every brand claim should map to something the code actually does:

| Brand claim | Mechanism in the repository |
|---|---|
| "Claims before edits" | Exclusive path leases in the broker and `agentterm`, and `Collaborate-Claim:` lines read by the `coordinate` check |
| "A record you can verify" | Hash-chained, signed log; `agentterm verify` checks it offline |
| "The merge stays human" | Required checks plus one human approval; agents cannot merge their own work |
| "Different agents" | 513 contacts: 11 guild agents, 43 tool mascots, 8 AI presets, 451 directory contacts |

If a feature is not implemented, it does not go in marketing copy. This follows the evidence rules in [`AGENTS.md`](../AGENTS.md).

## Visual system: two layers

The ChatGPT identity uses gold, midnight, and heraldry. The shipped app looks like an iOS group chat. The two do not compete, because each layer has its own territory:

- **The room** (the in-app chat, the Pages tour, docs) stays clean, glass-like, and iOS-inspired. Legibility comes first.
- **The guild** (crests, nameplates, character cards, posters, social posts) carries the fantasy art direction.

Gold is the bridge between them. In the room, gold appears only on identity: member nameplates, house badges, and the crest.

### Color roles

Each color has one meaning, used the same way in both layers.

| Token | Dark | Light | Meaning |
|---|---|---|---|
| `--brand-bg` | `#090E20` Midnight Realm | `#F5EBDD` Parchment | Page background |
| `--brand-surface` | `#15213B` Guild Navy | `#FFFFFF` | Cards and sheets |
| `--brand-identity` | `#EBC66E` Royal Gold | `#7A5C14` | **Who**: names, crests, house badges |
| `--brand-action` | `#0A62C4` | `#0A62C4` | **What you can do**: buttons and the outgoing bubble |
| `--brand-ai` | `#A78BFA` Arcane Violet | `#6D4FD8` | AI-generated content, model and agent markers |
| `--brand-link` | `#61DBF5` Cyber Cyan | `#08677F` | Links and technical highlights |
| `--brand-verified` | `#69D9A6` Emerald Forge | `#1A6B47` | Checks passed, claims granted |
| `--brand-danger` | `#FF777E` Ember Red | `#C2333B` | Errors and actions that need a decision |

Every text pair above meets WCAG 2.2 AA (at least 4.5:1). I measured the lowest pairs: violet on Guild Navy is 5.88:1, action blue with white text is 5.90:1, and violet on parchment is 4.74:1. **Never use color alone** to show approval, permission, or error state. Add a label or an icon.

> **Existing gap:** the app's outgoing bubble (`--outgoing: #0a84ff` in `web/dev-guild/app/globals.css`) carries white text at **3.65:1**, which fails AA for 16 px text. `#0A62C4` keeps the iMessage feel at 5.90:1. That change belongs in a separate PR to `web/dev-guild`.

The tokens are in [`brand/tokens.css`](brand/tokens.css).

### Typography

| Role | Typeface | Use |
|---|---|---|
| Ceremonial | **Cinzel** (700 / 600) | Wordmark, house names, character nameplates, poster and hero titles. Never in chat or body text. |
| Interface | **Inter** (system SF on Apple devices) | Chat, navigation, docs, buttons, and all body copy |
| Technical | **JetBrains Mono** | Paths, claims, commands, SHAs, log entries, version numbers |

All three are under the SIL Open Font License 1.1. Fallbacks are Georgia, the system sans-serif, and the system monospace.

| Style | Font | Size / weight / line height |
|---|---|---|
| Hero | Cinzel | 64 / 700 / 1.15 (mobile 32–40) |
| H1 | Cinzel | 48 / 700 / 1.2 |
| H2 | Cinzel | 36 / 600 / 1.25 |
| H3 | Inter | 28 / 600 / 1.35 |
| Body | Inter | 16 / 400 / 1.65 |
| Chat bubble | Inter | 16 / 400 / 1.5 |
| Member name | Inter | 16 / 700, in `--brand-identity` |
| Button | Inter | 14 / 600 |
| Code | JetBrains Mono | 14 / 400 / 1.6 |
| Metadata | JetBrains Mono | 12 / 500 / 1.5 |

Tracking is +0.10em for the wordmark and +0.025em for Cinzel headings. Keep body lines to about 50–75 characters. Use a 4-based spacing scale: 4, 8, 12, 16, 24, 32, 48, 64, 96.

### Crest

[`brand/crest.svg`](brand/crest.svg) is a shield holding a code chevron (`< / >`), flanked by two connector bars. It shows the guild as a shield around shared code, with agents plugging in from both sides. Use it gold on midnight, or midnight on parchment. Below 32 px, use the shield without the wordmark.

## The guild houses

The ChatGPT draft proposed 18 houses. Seventeen of them match the 17 directory sections that already exist, one to one, so the houses are a presentation layer over [`GUILD-DIRECTORY.md`](GUILD-DIRECTORY.md) and not a second catalog. Counts come from the repository.

| House | Directory section | Contacts | Hall |
|---|---|---|---|
| Codewrights | AI coding agents | 75 | The Forge |
| Model Mages | AI APIs & model providers | 32 | Astral Court |
| Seekers | Web search/scraping/research APIs | 24 | Map Room |
| Portal Engineers | Agent infra & browser automation | 24 | Gateway |
| Orchestrators | Agent frameworks | 24 | Council |
| Archivists | Memory & knowledge infra | 24 | Memory Vault |
| Sentinels | GitHub/code review/DevSecOps | 24 | Watchtower |
| Skywrights | Cloud hosting | 24 | Cloud Docks |
| Data Keepers | Databases/storage/backend | 24 | Vault |
| Contract Artisans | API dev & testing | 22 | Test Lab |
| Watchers | AI observability | 22 | Observatory |
| Clockwork Guild | Workflow automation/CI | 22 | Engine Room |
| Keykeepers | Auth & secrets | 22 | Gatehouse |
| Heralds | Communications APIs | 22 | Dispatch Hall |
| Chain Cartographers | Blockchain/Web3 APIs | 22 | Ledger Hall |
| Scholars | Scientific research & data APIs | 22 | Great Library |
| Media Artisans | AI media & document APIs | 22 | Atelier |
| **Companions** | Guild agents, tool mascots, AI presets (`lib/cast.js`) | 62 | Guild Hall |

Total: 451 directory contacts plus 62 companions, for 513. The ChatGPT draft counted 478 concepts because its roster was built before the directory was finalized. The directory wins.

Each character card carries the same fields: house, title ("The Terminal Scribe"), creature, personality (two words), signature item, and voice. Sample entries from the draft: OpenCode is a fox pathfinder, Codex CLI a cloud automaton scribe, ForgeCode a badger blacksmith, Firecrawl a fire-dragon cartographer, Vercel a black-raven sky architect, and ORCID an owl registrar.

## Voice

The voice is a sharp engineering team with the warmth of an adventuring party. Use fantasy in the story and plain language in the controls.

| In story and marketing | In controls, permissions, and evidence |
|---|---|
| Guild agent | Agent |
| Guild roster | Directory |
| Quest / task card | Task card (`handoff`) |
| Guild hall | Room |
| Guild class / house | Directory section |
| — | Review pending |
| — | Test not run |
| — | Human approval required |

Anything that touches security, authentication, approvals, or audit records stays literal. Use NOT RUN and UNVERIFIED exactly as `AGENTS.md` defines them. Never describe a claim or an approval in fantasy terms.

## Guardrails

- **Third-party names.** Many characters are named after real products (Vercel, Firecrawl, OpenCode, and others). Every place that shows the roster needs this notice: *"Characters are fan-made personas. Collaborate is not affiliated with or endorsed by the products named."* Character art should be original. Do not redraw a company's logo as a mascot. The current avatars in `assets/dev-guild/avatars/` and `web/dev-guild/public/avatars/` use real product marks, so they need a separate legal review.
- **Apple language.** In public marketing copy, describe the look as "glass-style" or "iOS-inspired". Do not call it "Liquid Glass" or "iPhone 18", because those are Apple's names. Engineering docs may describe the native reference client in `native/ios`, which sketches that glass-style chat UI (glass nav, solid bubbles, glass composer, translucent attachment sheet, plugin list, adaptive photo controls, system/light/dark, and reduced motion, transparency, and contrast, plus Dev Guild art). It has not been compiled. The free mobile path is the PWA.
- **Name clearance.** Before any registration, run a trademark and handle search for "The Dev Guild" and "Collaborate". Both are common words and other groups already use them. A distinct qualifier may be needed, such as "Collaborate Dev Guild" or a coined name.
- **The parent name.** "GoodShyt" reads as profanity to some enterprise and education buyers. Keep it on legal and company pages, and let **Collaborate** carry the product.
- **ChatGPT deliverables.** Sessions described a brand kit ZIP, a 478-character JSON file, and an interactive guide that are still **UNVERIFIED** here. A native iOS reference client now lives in `native/ios` (not compiled). Durable rooms, R2 attachments, and fail-closed ClamAV scanning are in `web/dev-guild` (PR 101); ClamAV itself is not hosted, and uploads stay disabled.

## Next steps

1. The maintainer approves or changes the names, taglines, and houses on this page.
2. A PR to `web/dev-guild` adopts `brand/tokens.css`, fixes the bubble contrast, and adds house badges to directory rows.
3. A PR to `site/index.html` adds the crest, the tagline, and the affiliation notice.
4. Character cards are added as structured fields on `web/dev-guild/data/contacts.json`, one house at a time, so the tests in `GUILD-DIRECTORY.md` keep guarding them.
5. Original character art, starting with the 11 guild agents.
