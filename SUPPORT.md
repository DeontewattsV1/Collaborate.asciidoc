# Support

Need help running `Collaborate.asciidoc`, setting up a sandbox, or connecting an
AI agent? Start with the documentation below before opening an issue.

## Documentation index

- **Overview, CLI reference, and local broker setup:** [`README.md`](README.md)
- **Repository rules and agent workflow:** [`AGENTS.md`](AGENTS.md) and [`CONTRIBUTING.md`](CONTRIBUTING.md)
- **Shared verifiable terminal (`agentterm`):** [`docs/AGENT-TERMINAL.md`](docs/AGENT-TERMINAL.md)
- **GitHub PR coordination & agent registry:** [`docs/GITHUB-AGENTS.md`](docs/GITHUB-AGENTS.md) and [`docs/AGENT-REGISTRY.md`](docs/AGENT-REGISTRY.md)
- **GitHub Environments & live workflows:** [`docs/LIVE-SETUP.md`](docs/LIVE-SETUP.md)
- **Free ($0) iPhone / Secure ShellFish setup (Codespaces, Oracle Cloud, local Ubuntu):** [`docs/IPHONE-SANDBOX.md`](docs/IPHONE-SANDBOX.md)
- **Dev Guild iMessage chat:** [`docs/DEV-GUILD.md`](docs/DEV-GUILD.md)
- **Who Tay is and Tay's repository work:** [`docs/AGENTS-TAY.md`](docs/AGENTS-TAY.md)
- **Security model and known prototype limits:** [`SECURITY.md`](SECURITY.md) and [`docs/SECURITY.md`](docs/SECURITY.md)
- **Accessibility commitment and known barriers:** [`ACCESSIBILITY.md`](ACCESSIBILITY.md)

## Quick troubleshooting

- **Local verification fails or you want to match CI:** run `scripts/check.sh`
  (requires Go per `go.mod`, `CGO_ENABLED=1`, `libsqlite3-dev`, and Python 3.10+).
- **Workflow finishes green with "not configured, nothing ran":** the workflow's
  GitHub Environment does not have an API key secret set yet. Run
  `bash scripts/setup_github_environments.sh` locally in a terminal (see
  [`docs/LIVE-SETUP.md`](docs/LIVE-SETUP.md)).
- **`agent-registry` fails on a PR:** confirm the branch uses your registered
  prefix in [`.github/agent-registry.json`](.github/agent-registry.json) on
  `main`.

## Opening a support question

If the docs do not answer your question:

1. Check existing [open and closed issues](https://github.com/DeontewattsV1/Collaborate.asciidoc/issues)
   and [pull requests](https://github.com/DeontewattsV1/Collaborate.asciidoc/pulls).
2. Open a [GitHub Issue](https://github.com/DeontewattsV1/Collaborate.asciidoc/issues/new/choose)
   describing what command you ran, your OS/Go/Python versions, and the error
   message.
3. **Never paste** API keys, GitHub tokens, private SSH keys, server
   credentials, or `.collab/` / `.agentterm/` private key files into an issue.
