# Multi-provider AI review bridge — installation and trust contract

> **Quick setup:** run `bash scripts/setup_github_environments.sh`. It creates all four environments below with `MODEL_NAME` pre-filled and prompts for each key. See [`LIVE-SETUP.md`](LIVE-SETUP.md).

**Target:** `DeontewattsV1/Collaborate.asciidoc` · **Status:** PROTOTYPE / NOT PRODUCTION-ATTESTED · **2026-11-23**  
**Research credit/provenance:** Deonté O'dell Watts, ORCID 0009-0005-8586-3650, GoodShyt Group Inc. · AI-assisted template, not independently validated.  
**Scope:** manual GitHub PR review only. No code editing, merging, branch deletion, self-approval, rule changes, deployment, model identity impersonation or credential exchange.

## Existing foundation to preserve

Do **not** replace `.github/workflows/agent-collaboration.yml`, `.github/workflows/collaborate-coordination.yml`, `.github/session-principals.json`, `scripts/session_hub.py`, `docs/AGENT-SESSION.md`, or the independently authenticated broker prototypes. This package **adds** a separate, deliberately manual model-execution lane. Existing Issue #34 handles GitHub-principal joins/heartbeats; do not treat model API credentials as a valid replacement for that identity protocol.

## Copy the files into the repo

Copy the package's `.github/workflows/` files, `scripts/agent_model_bridge.py` and `tests/test_agent_model_bridge.py` into the corresponding paths in the repository. Optionally copy this documentation to `docs/AGENT-MODEL-BRIDGE.md`. Use a small draft PR with `Collaborate-Claim:` lines for these files, follow `AGENTS.md`, and merge only after independent human review and mandatory CI. **This package has NOT been uploaded or committed to GitHub.**

## Create GitHub Environments *manually*

Go to **Repository → Settings → Environments** and create the **four environments below before enabling the workflow**. YAML `environment:` names reference environments; they **do not create their security settings, secret values, reviewer lists or external cloud IAM policies**. Unconfigured names could be created by GitHub without the protections you intended. The workflow refuses to call providers without their key/model values, but that is not a replacement for branch policies and reviewers.

| Environment | Environment **secret** | Environment **variable** | Provider endpoint (fixed in script) |
|---|---|---|---|
| `agent-openai` | `MODEL_API_KEY` = OpenAI API key | `MODEL_NAME` = authorized OpenAI Chat Completions model ID | `https://api.openai.com/v1/chat/completions` |
| `agent-anthropic` | `MODEL_API_KEY` = Anthropic API key | `MODEL_NAME` = authorized Claude model ID | `https://api.anthropic.com/v1/messages` |
| `agent-gemini` | `MODEL_API_KEY` = Google AI Studio Gemini Developer API key | `MODEL_NAME` = authorized Gemini model ID | `https://generativelanguage.googleapis.com/v1beta/models/{MODEL_NAME}:generateContent` |
| `agent-xai` | `MODEL_API_KEY` = xAI API key | `MODEL_NAME` = authorized xAI Chat Completions model ID | `https://api.x.ai/v1/chat/completions` |

**For every environment:** use deployment branches **Selected branches and tags → `main` only**; add an authorized required reviewer if the plan/features permit it; enable prevent self-review when a separate authorized human reviewer exists; disallow admin bypass where supported. Verify these settings in the GitHub UI. Configure API budgets and provider-side key restrictions. **Put keys at Environment level, not in repo-wide or organization-wide `MODEL_API_KEY` secrets**; broader secrets can be accessed by other workflows. Do not store keys in variables. Models use their own provider's access credentials, not each other's keys.

The workflow dispatcher is restricted to the verified `DeontewattsV1` GitHub actor and `main`. To permit additional human operators, make a protected, reviewed workflow change that explicitly authorizes their GitHub actor, plus appropriate environment reviewers. An AI speaking with a model key is **not** thereby an authorized GitHub actor. 

## Running it

1. After installation/merge and environment setup, choose **Actions → agent-model-bridge → Run workflow** on `main`.
2. Enter an **open PR number targeting `main`**. Choose `provider=openai` for a single inexpensive pilot, or `provider=all` to run OpenAI, Claude, Gemini and xAI. Choose `phase=review` first.
3. After you have checked providers, budgets and result quality, `phase=challenge` with `provider=all` makes each provider review the same snapshot, then see the **other** providers' model-generated findings and write a challenge. Single-provider `challenge` is refused rather than misrepresenting a self-review as independent.
4. Review the published, labeled **PR issue comment**. The original PR head SHA must still match; stale or incomplete snapshots fail closed. The comments do **not** count as a GitHub review approval or CI check.
5. Keep using Issue #34 for authenticated GitHub agent session membership and the existing path-lease coordination; models invoked by this workflow are **not** enrolled as verified GitHub principals. These are bounded one-off calls; they are **not** continuous agents.

**External data disclosure:** PR patches are transmitted to the selected provider(s). For a public repository they might still contain non-public data accidentally committed to a branch. Inspect the intended PR before approving external calls. Large PRs (>60 files or 48,000 patch characters), missing binary patches, moving heads, missing secrets/models or incomplete provider responses abort. No arbitrary code from the PR runs in a credentialed job. Artifacts are retained 7 days subject to repository settings.

## Separation of powers

| Stage | GitHub token | API key | GitHub side effect |
|---|---|---|---|
| `scope` | PR read | none | uploads bounded snapshot artifact |
| `review-*` | contents read (trusted checkout) | one environment-scoped provider key | creates review artifact only |
| `challenge-*` | contents read (trusted checkout) | one environment-scoped provider key | creates challenge artifact only |
| `publish` | issues write, PR read | **none** | adds one PR comment; cannot merge or push |
| `agent-model-bridge-tests` | contents read | none | validates standard-library tests without provider calls |

**Provenance limits:** A secret demonstrates possession of an API credential within a job, not that a named GitHub App or human authored the output. Results are unsigned and can be falsified if the workflow runner or artifact trust is compromised. A SHA-256 snapshot digest detects ordinary content changes, not a malicious actor able to rewrite both artifact content and digest. A model's finding is Tier T4 conjectural review evidence; external validation is **NOT RUN** until demonstrated. Tool permissions, successful API response, and generated criticism never satisfy independent human approval.

## Trial and validation

Locally:

```bash
python3 -m unittest discover -s tests -p 'test_agent_model_bridge.py' -v
python3 -m py_compile scripts/agent_model_bridge.py
```

The tests mock providers and GitHub API calls, so a local pass does not prove API key compatibility or GitHub Actions deployment protection. Review the workflow in a draft PR and use the no-secret `agent-model-bridge-tests` job. Once merged and environments are verified, dispatch **one** real review deliberately and verify that the artifact, current PR head, label and comment match. Cross-provider `challenge` performs two paid model rounds in **one** explicitly dispatched run; it does not need a prior run.

## Adversarial failure cases

- **Malicious PR prompts:** Treated as data; models may still be fooled, but they have no shell, no PR write token, and publish text is labeled untrusted. Human review required.
- **Secret leakage:** Jobs isolate individual provider API keys; provider error response bodies are never printed. This is not an OS-enforced per-process secret boundary inside a runner.
- **Stale review:** HEAD compared while scoping and again before posting; refuse if changed. This is not a cryptographic binding to actual execution on the head.
- **False peer identity:** Peer artifacts are labeled by matrix provider, not claimed as authenticated remote agent identities. No independent attestations.
- **Partial outputs:** Fail closed if requested providers are missing, or patch is incomplete; no successful publication.
- **Conflicting claims:** Present both, identify disagreement and perform an independent test; do not average divergent model opinions.

## Official documentation

- GitHub Environments: https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/manage-environments
- GitHub Actions least-privilege token: https://docs.github.com/en/actions/tutorials/authenticate-with-github_token
- OpenAI Chat Completions: https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create
- Anthropic API Overview: https://platform.claude.com/docs/en/api/overview
- Gemini API: https://ai.google.dev/gemini-api/docs/text-generation
- xAI Chat Completions: https://docs.x.ai/developers/model-capabilities/legacy/chat-completions

© 2026 Deonté O'dell Watts — ORCID 0009-0005-8586-3650 | GoodShyt Group Inc. | deonte@goodshyt.fun | Draft first articulated 2026-11-23. No novelty or patentability claim.
