---
name: self-improvement
description: "Captures learnings, errors, and corrections in .learnings/ so every agent working on this repository improves over time. Use when: (1) a command, workflow or check fails unexpectedly, (2) the maintainer or another agent corrects you, (3) someone asks for a capability that does not exist, (4) an external API or tool fails, (5) your knowledge turns out to be outdated, (6) you find a better approach to a recurring task. Review .learnings/ before major tasks."
---

# Self-improvement (Collaborate.asciidoc)

Adapted for this repository from the `self-improvement` skill by pskoett
(https://github.com/pskoett/pskoett-ai-skills/tree/main/skills/self-improvement).
OpenClaw-specific setup, hook scripts and the skill-extraction helper are not
included here; everything below works with plain files.

## Where things go

| Situation | Log to |
|---|---|
| Command, CI check or workflow fails | `.learnings/ERRORS.md` |
| You were corrected | `.learnings/LEARNINGS.md`, category `correction` |
| Knowledge was outdated | `.learnings/LEARNINGS.md`, category `knowledge_gap` |
| Better approach found | `.learnings/LEARNINGS.md`, category `best_practice` |
| Requested capability is missing | `.learnings/FEATURE_REQUESTS.md` |
| Same issue seen again | Link with `See Also`, bump priority, increment `Recurrence-Count` |
| Broadly applicable rule | Promote a one-line rule to `AGENTS.md` (shared by every agent) |

`.learnings/` is tracked in the repository so learnings are shared across
Claude, Codex, Manus, Gemini and human contributors. Never log secrets, tokens,
API keys, private data or unpublished research (see `AGENTS.md`).

## Entry formats

IDs: `TYPE-YYYYMMDD-XXX` with TYPE `LRN`, `ERR` or `FEAT`.

```markdown
## [LRN-YYYYMMDD-XXX] category
**Logged**: ISO-8601 · **Priority**: low|medium|high|critical · **Status**: pending · **Area**: backend|infra|tests|docs|config
### Summary
One line.
### Details
What happened, what was wrong, what is correct.
### Suggested Action
Concrete fix or rule.
### Metadata
- Source: conversation | error | user_feedback | agent_review
- Related Files: path
- See Also: ID
- Pattern-Key: stable.key (optional) · Recurrence-Count: N · First-Seen / Last-Seen
```

```markdown
## [ERR-YYYYMMDD-XXX] command_or_check
**Logged**: … · **Priority**: … · **Status**: … · **Area**: …
### Summary
### Error
(actual message)
### Context
### Suggested Fix
### Metadata
- Reproducible: yes|no|unknown · Related Files · See Also
```

```markdown
## [FEAT-YYYYMMDD-XXX] capability
**Logged**: … · **Priority**: … · **Status**: … · **Area**: …
### Requested Capability
### User Context
### Complexity Estimate
### Suggested Implementation
### Metadata
- Frequency: first_time|recurring · Related Features
```

## Resolving and promoting

- Fixed: set `Status: resolved` and add a `### Resolution` block (date, commit/PR, note).
- Other statuses: `in_progress`, `wont_fix` (give the reason), `promoted`.
- Promote when a learning applies beyond one change, prevents a recurring
  mistake, or documents a convention. Write it into `AGENTS.md` as a short
  prevention rule, then set `Status: promoted` and `Promoted: AGENTS.md`.
- Recurring patterns (`Recurrence-Count >= 3`, two or more tasks, 30 days)
  must be promoted.

## Triggers

Log when you see: "No, that's wrong…", "Actually…", "That's outdated…" (correction);
"Can you also…", "Is there a way to…" (feature request); a non-zero exit code,
stack trace, failing check or unexpected output (error); information you did
not know, or docs/API behaving differently than you expected (knowledge gap).

## Review

Before a major task, and after finishing one:

```bash
grep -h "Status\*\*: pending" .learnings/*.md | wc -l      # open items
grep -B3 "Priority\*\*: high" .learnings/*.md | grep "^## \["  # high priority
grep -rn "Pattern-Key" .learnings/                         # recurring patterns
```

Resolve what is fixed, link duplicates, promote what recurs.
