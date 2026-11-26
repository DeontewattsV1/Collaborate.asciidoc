---
name: automate-recurring-errors
description: "Turn a mistake that keeps happening into an automated guard (a test or a CI hygiene rule) and prove the guard against a commit that had the mistake. Use when the same CI failure, review finding, or repository-shape mistake comes up a second time."
---

# Automate Recurring Errors

A written rule does not stop a mistake from recurring. A guard in the required check does. Use this skill whenever the same failure happens twice.

## Quick Reference

| Situation | Action |
|-----------|--------|
| The mistake is a repository shape (file names, workflow structure, missing artifact) | Add a rule to `scripts/repo_hygiene.py`, which runs in `go-verify` through `scripts/check.sh` |
| The mistake is code or policy behaviour | Add a Go or Python test next to the code; it runs through `scripts/check.sh` |
| Validating the guard | Run it on a historical commit that had the bug: it must flag the bug. Run it on current `main`: it must be clean |
| Recording the rule | Add a short line to `AGENTS.md` and `docs/ENGINEERING-NOTES.md` |

## Usage

1. **Describe the exact bad input.** A file name, a workflow shape, a command, a response body. If you cannot state it precisely, the guard will be too broad.
2. **Write the smallest check** that fails on that input and on nothing else. Put the explanation in the error message, not in a separate document.
3. **Write tests** that replay the real bad inputs, plus one test that the current repository passes.
4. **Prove it on history:**

   ```bash
   git worktree add --detach /tmp/hist <bad-commit>
   python3 scripts/repo_hygiene.py /tmp/hist   # must report the known problems
   git worktree remove --force /tmp/hist
   python3 scripts/repo_hygiene.py .           # must be clean
   ```

5. **Wire it into** `scripts/check.sh` so local runs and CI stay identical.
6. **Record the rule** in `AGENTS.md` and `docs/ENGINEERING-NOTES.md`.

## Example

`scripts/repo_hygiene.py` catches two recurring repository-shape mistakes: a tracked path whose name looks like a pasted shell command, and a workflow file that is not a workflow or that builds a missing Dockerfile. Its tests replay the exact bad file names and workflow snippets, and a separate test asserts that the current repository is clean.

## Guardrails

- Do not add a guard that only restates a rule already enforced elsewhere.
- Do not weaken an existing check to make the new one pass.
- Keep the error message actionable: say what is wrong and what to do instead.
