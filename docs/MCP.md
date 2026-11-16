# MCP integration

`collab mcp` is a JSON-RPC 2.0 stdio server implementing the MCP `initialize`, `ping`, `tools/list`, and `tools/call` method surface for the coordination tools. Each coding assistant runs its **own** `collab mcp` process, but they all point at one broker URL. No model weights, provider keys, or hosted-model credentials pass through the broker.

Example configuration shape (adapt `command` to your installation and use your MCP host's **secret environment injection**; do not commit values):

```json
{
  "mcpServers": {
    "collab": {
      "command": "/absolute/path/to/collab",
      "args": ["mcp"],
      "env": {
        "COLLAB_URL": "http://127.0.0.1:8787"
      }
    }
  }
}
```

`COLLAB_TOKEN` must be supplied out-of-band via the process environment. If your MCP host replaces rather than inherits environment variables, configure its secret-store feature to inject that token. Never put the token inside the JSON file.

Recommended operational instructions for each model:

1. Register a unique agent identifier at the start of a session, labeling the model honestly as unverified metadata.
2. Read `collab_events` with an `after` cursor frequently, and send `collab_say` when coordinating interfaces, blockers, reviews, or decisions. For immediate terminal notification, run `collab watch` in a separate terminal.
3. Before editing, request `collab_claim` for specific paths. Renew long tasks before TTL expiry; respect conflicts and release leases when finished. Work only within your own Git worktree.
4. Run actual test gates in a terminal with `collab gate`; use `collab receipt` to derive changed files from the local Git commit. An MCP `collab_receipt` is a self-reported claim, not independent commit verification.
5. Do not use this coordination channel to transfer raw tokens, system prompts, private keys, or proprietary source without permission.

The MCP adapter does not automatically create a model session or grant tools inside proprietary products. The model host must support stdio MCP configuration and its local terminal must be allowed to reach the broker.
