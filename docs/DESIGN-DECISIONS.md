# Design notes

These are the choices behind the broker, the terminal, and the group chat. They are recorded so a future change does not accidentally undo one of them.

## The broker mediates side effects

Agents do not edit the integration branch directly. They work in isolated Git worktrees, submit edits through a lease-checked endpoint, and let the broker derive a receipt from the server-side checkout. Keeping Git integration outside the model's direct control makes the merge boundary inspectable.

This follows the published pattern guidance in [Agentic Atlas: Effect Discipline](https://agentic-atlas.dev/nodes/effect-discipline#model-and-claims). It is prior art, not a novelty claim.

## Verification is asymmetric

A broker can prove that an event was recorded and signed. It cannot prove that an agent's self-reported gate output is true, that a model produced a given answer, or that a commit exists on a remote. Those require independent CI, protected branches, and per-agent credentials.

The project keeps deterministic ledger checks separate from real goal validation and independently executed tests, consistent with [Agentic Atlas: Verification Asymmetry](https://agentic-atlas.dev/nodes/verification-asymmetry#model-and-claims).

## Claims are hierarchical and expiring

A claim on `internal/parse` conflicts with `internal/parse/token.go` and with `internal`, but not with `internal/parser2`. Leases expire, can be renewed only while live, and are checked again when a receipt or commit is submitted. The same shape appears in three places: broker leases, `agentterm` claims, and GitHub PR path leases. A team can move between surfaces without relearning the rule.

## The room is the coordination surface

A pull request records the result, not the conversation. The group chat records intent, handoffs, and review findings in the same thread where people already talk. With the optional VM bridge connected, chat lines become `chat` entries in the guild's signed log, so the conversation is part of the evidence rather than a separate transcript. The standalone app keeps its transcript in server state.

## Evidence is tamper-evident, not tamper-proof

Each ledger entry links to the previous hash and carries a signature. The terminal re-hashes entries in the browser and can verify an exported log offline. A host that controls the database can still delete the file or withhold events, so the head hash should be pinned somewhere independent when the history matters.

## What this is not

- Not a sandbox for hostile code. Gates and allowlisted commands run on the host and can reach the host filesystem and network.
- Not a model-identity system. A shared GitHub account can clock in under any registered branch prefix. Per-agent GitHub Apps or machine accounts are the fix.
- Not a merge queue for a large team. It is a small, reviewable coordination layer with a human merge boundary.
- Not production-ready. See [`SECURITY.md`](SECURITY.md) for the verified properties and the remaining gaps.
