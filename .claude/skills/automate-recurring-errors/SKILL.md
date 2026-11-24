---
name: automate-recurring-errors
description: "Turn a mistake that keeps happening into an automated guard (a test or a CI hygiene rule) and prove the guard against the real commits where the mistake occurred. Use when a .learnings/ Pattern-Key reaches two or more occurrences, or when the same CI failure or review finding comes up again."
---

# Automate Recurring Errors

A written rule did not stop a mistake from happening three times in this repository. A guard in the required check did. Use this skill whenever a logged error recurs.

## Quick Reference

| Situation | Action |
|-----------|--------|
| A `Pattern-Key` has `Recurrence-Count >= 2` | Write a guard; do not add another note |
| The mistake is a repository shape (file names, workflow structure, missing artifact) | Add a rule to `scripts/repo_hygiene.py`, which runs in `go-verify` through `scripts/check.sh` |
| The mistake is code or policy behaviour | Add a Go or Python test next to the code; it runs through `scripts/check.sh` |
| Validating the guard | Run it on a historical commit that had the bug: it must flag the bug. Run it on current `main`: it must be clean. |
| Recording it | Add a `Systemic fix` line to the error's Resolution block, and promote a one-line rule to `AGENTS.md` |

## Usage

1. **Find candidates:** `grep -rn "Pattern-Key" .learnings/` and look for counts of 2 or more.
2. **Write the smallest check** that fails on the exact bad input. Name the learning ID in its error message, so whoever trips it can find the history.
3. **Write tests** that replay the real bad inputs (exact file names and file contents), plus a test that the current repository passes.
4. **Prove it on history:**
   ```bash
   git worktree add --detach /tmp/hist <bad-commit>
   python3 scripts/repo_hygiene.py /tmp/hist   # must report the known problems
   git worktree remove --force /tmp/hist
   python3 scripts/repo_hygiene.py .           # must be clean
   ```
5. **Wire it into** `scripts/check.sh`, so that local runs and CI stay identical.
6. **Update `.learnings/`** (`Systemic fix`) and `AGENTS.md`.

## Examples

`scripts/repo_hygiene.py` (PR #74) came from `ops.command_pasted_as_file` (3 occurrences) and `ops.template_committed_unadapted` (2 occurrences). Replayed on `main@79cb659`, it reported 7 problems; on `main@ea6bcd8`, 4 problems, which were every real incident. On current `main` it reported 0.

## Source Learning

This skill was extracted from a learning entry.
- Learning ID: LRN-20261124-003
- Original File: .learnings/LEARNINGS.md
