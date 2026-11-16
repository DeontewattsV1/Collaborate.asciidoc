# Design decisions and provenance

**Date:** 2026-11-16. **Scope:** original engineering scaffold for the requested multi-model terminal collaboration protocol.

- Design: a broker mediates messages and lease requests; isolated Git worktrees carry source edits; an evidence log provides receipt replay. Provenance: PI request + AI-assisted engineering.
- Recovery rule: keep side-effecting Git integration outside the model's direct control. This is consistent with [Agentic Atlas: Effect Discipline § Model and Claims](https://agentic-atlas.dev/nodes/effect-discipline#model-and-claims). Published pattern guidance, **not** a novelty claim.
- Verification rule: separate deterministic broker ledger checks from real goal validation and independently executed tests, consistent with [Agentic Atlas: Verification Asymmetry § Model and Claims](https://agentic-atlas.dev/nodes/verification-asymmetry#model-and-claims). Published pattern guidance, **not** proof of the implementation's overall correctness.
- [DERIVED / T2, local test scope] One SQLite transaction serializes simultaneous conflicting claims. Falsifier: a concurrent test in which two holders obtain overlapping unexpired exclusive leases.
- [DERIVED / T2, local test scope] An edited journal record fails pinned-key signature verification. Falsifier: a modified event that passes `collab verify` without key compromise.
- [CONJECTURE / T3, untested] With per-agent attestation, OS-enforced edit isolation, and protected merge policy, the system could produce a reliable externally verifiable multi-agent contribution history. Falsifier: find a path through those controls that lands changes without an attributable valid receipt.
- **Novelty:** no standalone novelty or priority established. Distributed messaging, event sourcing, SQLite transactions, Ed25519 signing, MCP, and Git worktrees have established prior art. The specific integration has not undergone prior-art search or independent evaluation.

© 2026 Deonté O'dell Watts — ORCID 0009-0005-8586-3650 | GoodShyt Group Inc. | AI-assisted development; no claim of independently verified novelty or publication approval.
