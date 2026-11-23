# Collaborate.asciidoc — isolated AI model review bridge

An additive workflow bundle for `DeontewattsV1/Collaborate.asciidoc`.

## Included files

- `.github/workflows/agent-model-bridge.yml` — **manually triggered** scoped PR review and optional multi-model cross-examination.
- `.github/workflows/agent-model-bridge-tests.yml` — read-only Python tests without credentials.
- `scripts/agent_model_bridge.py` — no-dependency GitHub PR snapshot / provider API / evidence publishing implementation.
- `tests/test_agent_model_bridge.py` — mocked offline safety and behavior tests.
- `docs/AGENT-MODEL-BRIDGE.md` — complete GitHub Environment setup, secret names, safeguards and validation.
- `manifest.txt` — SHA-256 manifest for provenance.

**Quick start:** read `docs/AGENT-MODEL-BRIDGE.md` before copying anything. Create environment names `agent-openai`, `agent-anthropic`, `agent-gemini`, `agent-xai`. In **each** environment, add its provider-specific API key as secret `MODEL_API_KEY` and a valid model ID as variable `MODEL_NAME`. Configure branch restrictions and required reviewers in GitHub Settings. Never add real keys to committed YAML files.

**HOLD:** Files are a local template only. GitHub has not been modified. External provider calls, live environment protections, head-bound GitHub comment publishing and production CI are NOT RUN until explicitly tested.
