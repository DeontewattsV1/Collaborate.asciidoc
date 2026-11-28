# Support

Need help running `Collaborate.asciidoc`, setting up a sandbox, or connecting an AI agent? Start with the documentation below before opening an issue.

## Documentation index

- **Overview and workflow:** [`README.md`](README.md)
- **Command reference and local broker walkthrough:** [`docs/CLI.md`](docs/CLI.md)
- **Current Fly hosting, costs, and deploy/rollback:** [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md)
- **Broker remote TLS, backup, and trust boundaries:** [`docs/OPERATIONS.md`](docs/OPERATIONS.md)
- **Broker ledger and event types:** [`docs/BROKER-PROTOCOL.md`](docs/BROKER-PROTOCOL.md)
- **Repository rules and agent workflow:** [`AGENTS.md`](AGENTS.md) and [`CONTRIBUTING.md`](CONTRIBUTING.md)
- **Shared verifiable terminal (`agentterm`):** [`docs/AGENT-TERMINAL.md`](docs/AGENT-TERMINAL.md)
- **GitHub PR coordination and agent registry:** [`docs/GITHUB-AGENTS.md`](docs/GITHUB-AGENTS.md) and [`docs/AGENT-REGISTRY.md`](docs/AGENT-REGISTRY.md)
- **GitHub Environments and live workflows:** [`docs/LIVE-SETUP.md`](docs/LIVE-SETUP.md)
- **Optional iPhone or Secure ShellFish sandbox:** [`docs/IPHONE-SANDBOX.md`](docs/IPHONE-SANDBOX.md)
- **Dev Guild group chat:** [`docs/DEV-GUILD.md`](docs/DEV-GUILD.md)
- **Security model and prototype limits:** [`SECURITY.md`](SECURITY.md) and [`docs/SECURITY.md`](docs/SECURITY.md)
- **Accessibility commitment and known barriers:** [`ACCESSIBILITY.md`](ACCESSIBILITY.md)
- **Durable engineering rules:** [`docs/ENGINEERING-NOTES.md`](docs/ENGINEERING-NOTES.md)

## Quick troubleshooting

- **Local verification fails or you want to match CI:** run `scripts/check.sh` (requires Go per `go.mod`, `CGO_ENABLED=1`, `libsqlite3-dev`, and Python 3.10+).
- **A workflow finishes green with "not configured, nothing ran":** the workflow's GitHub Environment does not have an API key secret yet. Run `bash scripts/setup_github_environments.sh` in a terminal (see [`docs/LIVE-SETUP.md`](docs/LIVE-SETUP.md)).
- **`agent-registry` fails on a PR:** confirm the branch uses a registered prefix in [`.github/agent-registry.json`](.github/agent-registry.json) on `main`.
- **`coordinate` reports a lease conflict:** an earlier open PR holds a path you changed. Drop that path, narrow the change, or ask the holder for a handoff.

## Opening a support question

If the docs do not answer your question:

1. Check existing [open and closed issues](https://github.com/DeontewattsV1/Collaborate.asciidoc/issues) and [pull requests](https://github.com/DeontewattsV1/Collaborate.asciidoc/pulls).
2. Open a [GitHub Issue](https://github.com/DeontewattsV1/Collaborate.asciidoc/issues/new/choose) describing what you ran, your OS, Go and Python versions, and the error message.
3. **Never paste** API keys, GitHub tokens, private SSH keys, server credentials, or `.collab/` / `.agentterm/` private key files into an issue.
