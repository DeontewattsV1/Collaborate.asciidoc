# Live MCP model-client pilot

## Result

A disposable live round-trip succeeded using two models from the sandbox’s OpenAI-compatible model proxy: OpenAI **GPT-6 Luna** as agent `alpha` and Google **Gemini 3.8 Flash** as agent `beta`. Each ran as a separate Unix user with its own Ed25519 identity, private `.collab` configuration, and private workspace. The host harness started one `collaborate mcp` stdio process per identity, performed MCP `initialize`/`tools/list`, and passed only MCP-discovered function schemas to that agent’s selected model.

GPT-6 Luna called `collaborate_say` with a synthetic marker. Gemini 3.8 Flash called `collaborate_inbox`, received that marker from the broker, then called `collaborate_say` with a reply. The broker recorded two independently signed events and `collaborate verify` reported `checked: 2, ok: true`. No source files, user data, or application secrets were included in the model prompts. The test used real inference through the sandbox proxy, not direct provider API accounts and not a third-party IDE/assistant host.

The host offered no shell, arbitrary HTTP, or filesystem tools. Its MCP capability set was exactly `collaborate_claim`, `collaborate_renew`, `collaborate_release`, `collaborate_say`, `collaborate_inbox`, and `collaborate_receipt`; a `shell_exec` call was rejected by the server. The live test further restricted the sender model to `collaborate_say`, and the responder to `collaborate_inbox` followed by `collaborate_say`. The responder’s inbox result was included in its model prompt, so inbox content is disclosed to that configured model endpoint; this run used synthetic content only.

## Permission evidence

For both Unix users, the live process could read and write its own workspace. Attempts to list or write the sibling agent directory were denied; reading the sibling Ed25519 private-key file was denied. Listing the configured broker-private directory and reading the configured SQLite database path were also denied. These probes establish denial for those exact paths, not a blanket OS sandbox guarantee. Per-agent directories were mode `0700`, agent keys and proxy credential files mode `0600`, and the broker state directory was inaccessible to either agent account. The temporary users, credentials, broker, and test files were removed after the run.

The included [`mcp_live_client.py`](../scripts/mcp_live_client.py) is a small repeatable model-client harness. It reads a temporary JSON credentials file containing `api_base` and `api_key`; the file must be regular, owned by the current UID, and inaccessible to group/other. It rejects cleartext remote endpoints and does not follow HTTP redirects. It does not enforce an endpoint hostname allowlist, so only use a trusted `api_base`. It obtains tools from the MCP server, executes only its fixed MCP subcommand, and starts that broker-facing subprocess with a reduced environment that excludes the provider key. The host and child still share a UID, so this is not a protection from compromised same-UID code. Never commit or share a credentials file; remove it after use. The two pilot accounts used the same sandbox proxy credential; the account separation test proves file-permission isolation between those Unix users, not separate billing/provider identities.

## Reproduce

Build the CLI and register two agents in one local test session using the normal setup in the repository README. Give each agent a separate Unix account and worktree. Place only that agent’s `.collab/config.json` and `keys/<agent>.key` in its private directory. Create a temporary JSON credential file owned by that account with `api_base` set to the sandbox’s `OPENAI_API_BASE` (or `OPENAI_BASE_URL`) and `api_key` set to its injected `OPENAI_API_KEY`. Do not put a key in a command-line argument or repository file.

Run the sender under its dedicated account:

```sh
python3 scripts/mcp_live_client.py \
  --role sender --agent alpha --model gpt-6-luna \
  --repo /private/alpha --mcp-binary /usr/local/bin/collaborate \
  --credentials-file /private/alpha/provider.json \
  --marker SYNTHETIC-ROUND-TRIP-ONLY \
  --own-workdir /private/alpha/worktree \
  --peer-workdir /private/beta \
  --peer-key /private/beta/.collab/keys/beta.key \
  --broker-private /private/broker \
  --broker-db /private/broker/state.sqlite
```

Then run the responder under its own account with `--role responder`, agent `beta`, model `gemini-3.8-flash`, its own repo and credentials, and the same marker. The script assumes a local loopback broker session already exists and both agents are registered. The example models are current choices from the live catalog at the time of this test; select only IDs the active catalog advertises with tool support.

## Limits

This validates the custom harness against the MCP stdio bridge and two real model endpoints. It does **not** validate Claude Code, Gemini CLI, Cursor, or another third-party host’s MCP configuration. It does not prove that model identity is authentic, provider labels are attested, or model output is safe.

The execution boundary is Unix discretionary file permissions, not a hostile-code sandbox. Each model host and its MCP child share that agent’s Unix UID; code compromised within that UID may access that agent’s own key/workspace and may inspect its own process state. Network access and process creation are not disabled at the OS level. The test did not use containers, seccomp, namespaces, or per-agent network policy. Do not run untrusted models against sensitive repositories on the basis of this pilot alone.
