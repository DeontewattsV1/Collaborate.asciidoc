#!/usr/bin/env python3
"""Reconcile a repository-wide, GitHub-principal-authenticated agent session.

Only the trusted default-branch GitHub Actions workflow runs this program.
GitHub comment metadata authenticates an account, NOT an AI provider/model.
"""
from __future__ import annotations

import argparse
import hashlib
import html
import json
import os
import re
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

MARKER = "<!-- collaborate-session-hub:v1 -->"
COMMAND = "Collaborate-Session: "
NAME = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$")
SHA = re.compile(r"^[0-9a-f]{40}$")
HUB_ISSUE = 34
TTL = timedelta(hours=2)
MAX_PAGES = 30


def utc(value: str) -> datetime:
    return datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(timezone.utc)


def clean(s: str) -> str:
    return html.escape(s, quote=True).replace("|", "\\|").replace("`", "'")


def parse_event(comment: dict) -> dict | None:
    """Accept only a never-edited, one-line JSON declaration by an API author."""
    user = comment.get("user") or {}
    if type(user.get("id")) is not int or not user.get("login") or user.get("type") not in {"Bot", "User"}:
        return None
    if comment.get("created_at") != comment.get("updated_at"):
        return None  # Edited comments cannot rewrite an earlier session event.
    raw = comment.get("body") or ""
    if not raw.startswith(COMMAND) or "\n" in raw or len(raw) > 2048:
        return None
    try:
        val = json.loads(raw[len(COMMAND):])
    except (ValueError, TypeError):
        return None
    if not isinstance(val, dict) or set(val) - {"v", "action", "instance", "role", "pr", "head"}:
        return None
    if type(val.get("v")) is not int or val["v"] != 1 or val.get("action") not in {"join", "heartbeat", "leave"}:
        return None
    if not isinstance(val.get("instance"), str) or not NAME.fullmatch(val["instance"]):
        return None
    role = val.get("role", "unknown")
    if not isinstance(role, str) or not NAME.fullmatch(role):
        return None
    pr = val.get("pr")
    if pr is not None and (type(pr) is not int or not 1 <= pr <= 100000000):
        return None
    head = val.get("head")
    if head is not None and (not isinstance(head, str) or not SHA.fullmatch(head)):
        return None
    try:
        when = utc(comment["created_at"])
    except (KeyError, TypeError, ValueError):
        return None
    return {"id": comment["id"], "actor_id": user["id"], "login": user["login"],
            "type": user["type"], "when": when, "event": val}


def parse_policy(policy: dict) -> dict[int, dict]:
    if not isinstance(policy, dict) or policy.get("version") != 1 or not isinstance(policy.get("principals"), list):
        raise ValueError("invalid session identity policy")
    output = {}
    for p in policy["principals"]:
        if not isinstance(p, dict) or set(p) != {"id", "login", "type", "label"}:
            raise ValueError("invalid principal policy entry")
        if type(p["id"]) is not int or p["id"] < 1 or p["id"] in output:
            raise ValueError("duplicate or invalid GitHub principal ID")
        if p["type"] not in {"Bot", "User"} or not all(isinstance(p[x], str) and p[x] for x in ("login", "label")):
            raise ValueError("invalid principal metadata")
        output[p["id"]] = p
    return output


def reconcile(comments: list[dict], policy: dict[int, dict], now: datetime) -> tuple[list[dict], list[dict], str]:
    """Deterministically replay GitHub comment events, preserving principal scope."""
    events = [e for c in comments if (e := parse_event(c)) is not None]
    events.sort(key=lambda e: (e["when"], e["id"]))
    active: dict[tuple[int, str], dict] = {}
    unrecognized = []
    accepted = []
    for e in events:
        if e["when"] > now + timedelta(minutes=2):
            continue
        principal = policy.get(e["actor_id"])
        if not principal or e["login"].lower() != principal["login"].lower() or e["type"] != principal["type"]:
            if e["event"]["action"] == "join":
                unrecognized.append(e)
            continue
        accepted.append(e)
        key = (e["actor_id"], e["event"]["instance"])
        event_type = e["event"]["action"]
        prev = active.get(key)
        if event_type == "join":
            # A new join after leave or expiry starts a new continuous episode.
            # A repeated join while still live retains the current episode start.
            started = (prev["first_seen"] if prev and e["when"] <= prev["last_seen"] + TTL
                       else e["when"])
            active[key] = {**e, "first_seen": started, "last_seen": e["when"]}
        elif event_type == "heartbeat" and prev and e["when"] <= prev["last_seen"] + TTL:
            # Heartbeat certifies only continued presence of this GitHub principal.
            # It cannot rewrite the role, PR or head claimed by the latest join.
            active[key] = {**prev, "last_seen": e["when"], "id": e["id"]}
        elif event_type == "leave":
            active.pop(key, None)
    participants = []
    for p in active.values():
        if now <= p["last_seen"] + TTL:
            participants.append({**p, "principal": policy[p["actor_id"]]})
    participants.sort(key=lambda x: (x["principal"]["label"], x["actor_id"], x["event"]["instance"]))
    unrecognized = sorted(unrecognized, key=lambda x: x["id"])[-10:]
    attested = [dict(id=e["id"], actor_id=e["actor_id"], login=e["login"],
                     when=e["when"].isoformat(), event=e["event"]) for e in accepted]
    digest = hashlib.sha256(json.dumps(attested, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    return participants, unrecognized, digest


def render(participants: list[dict], unrecognized: list[dict], digest: str, prs: list[dict], repo: str) -> str:
    lines = [MARKER, "## Collaboration session — GitHub-verified account principals", "",
             f"**Active instances:** {len(participants)} · **event snapshot SHA-256:** `{digest}`", "",
             "A GitHub API account ID establishes the posting principal, **not** the underlying AI model. "
             "Provider and instance labels are self-reported. Joining grants no new permission.", "",
             "| GitHub principal | Role (unverified) | Instance | Last event (UTC) | PR/head assertion |",
             "|---|---|---|---|---|"]
    for p in participants:
        data = p["event"]
        bound = f"#{data['pr']}" if data.get("pr") else "—"
        if data.get("head"):
            bound += f" / `{data['head'][:12]}`"
        lines.append(f"| `{clean(p['login'])}` (ID {p['actor_id']}; {clean(p['principal']['label'])}) | "
                     f"{clean(data['role'])} | `{clean(data['instance'])}` | {p['last_seen'].isoformat()} | {bound} |")
    if not participants:
        lines.append("| — | — | — | — | — |")
    lines += ["", "An instance expires after **2 hours** without a fresh joined or heartbeat event. "
              "A later `join` revives it; `leave` ends it immediately. GitHub comment edits are ignored.", "",
              f"### Open work ({len(prs)} PRs)", "",
              "| PR | Head SHA | Base | Draft |", "|---|---|---|---|"]
    for p in sorted(prs, key=lambda v: v["number"])[:40]:
        lines.append(f"| [#{p['number']}](https://github.com/{repo}/pull/{p['number']}) "
                     f"{clean(p['title'][:75])} | `{p['head']['sha'][:12]}` | "
                     f"`{clean(p['base']['ref'])}` | {'yes' if p['draft'] else 'no'} |")
    if len(prs) > 40:
        lines.append(f"| +{len(prs)-40} more (see GitHub Pull requests) | — | — | — |")
    if unrecognized:
        lines += ["", f"**Unrecognized join declarations:** {len(unrecognized)} recent event(s); "
                  "not enrolled or authorized. Verify identity against GitHub before modifying the trusted policy."]
    lines += ["", "**Handoff:** Use review comments and the PR's required checks for evidence. "
              "Claims, labels, and asserted SHAs in session comments are not certified results. "
              "Inspect actual protected-branch rules before merging.", "",
              "**Provenance limit:** This SHA-256 fingerprint covers the retrieved accepted events, "
              "not an append-only notarization. GitHub comments can be deleted, and an agent can reconnect "
              "only after posting another authenticated event.", ""]
    return "\n".join(lines)


class GitHub:
    def __init__(self, repo: str, token: str, api_base: str = "https://api.github.com") -> None:
        if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repo):
            raise ValueError("invalid GitHub repository")
        if not token:
            raise ValueError("GITHUB_TOKEN missing")
        self.repo, self.token, self.base = repo, token, api_base.rstrip("/")

    def request(self, path: str, method: str = "GET", body: dict | None = None):
        data = json.dumps(body).encode() if body is not None else None
        req = Request(self.base + f"/repos/{self.repo}" + path, data=data, method=method,
                      headers={"Accept": "application/vnd.github+json",
                               "X-GitHub-Api-Version": "2022-11-28", "User-Agent": "collaborate-session-hub",
                               "Authorization": "Bearer " + self.token})
        try:
            with urlopen(req, timeout=20) as resp:
                return json.loads(resp.read(8 * 1024 * 1024))
        except (HTTPError, URLError) as e:
            raise RuntimeError(f"GitHub API request failed ({method} {path}): {getattr(e, 'code', type(e).__name__)}") from e

    def pages(self, path: str) -> list[dict]:
        result = []
        for page in range(1, MAX_PAGES + 1):
            separator = "&" if "?" in path else "?"
            part = self.request(f"{path}{separator}per_page=100&page={page}")
            if not isinstance(part, list):
                raise ValueError("GitHub page did not return a list")
            result.extend(part)
            if len(part) < 100:
                return result
        raise RuntimeError(f"GitHub pagination limit ({MAX_PAGES}) reached; cannot attest complete state")

    def upsert(self, issue: int, rendered: str, comments: list[dict]):
        bots = [c for c in comments if c.get("body", "").startswith(MARKER)
                and (c.get("user") or {}).get("login", "").lower() == "github-actions[bot]"]
        if bots:
            current = bots[-1]
            if current["body"] != rendered:
                self.request(f"/issues/comments/{current['id']}", "PATCH", {"body": rendered})
        else:
            self.request(f"/issues/{issue}/comments", "POST", {"body": rendered})


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", required=True)
    parser.add_argument("--hub", type=int, default=HUB_ISSUE)
    parser.add_argument("--policy", type=Path, default=Path(__file__).resolve().parents[1] / ".github/session-principals.json")
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    if args.hub != HUB_ISSUE:
        raise ValueError("Hub issue must match the audited policy-bound issue")
    policy = parse_policy(json.loads(args.policy.read_text(encoding="utf8")))
    client = GitHub(args.repo, os.environ.get("GITHUB_TOKEN", ""), os.environ.get("GITHUB_API_URL", "https://api.github.com"))
    comments = client.pages(f"/issues/{args.hub}/comments")
    prs = client.pages("/pulls?state=open")
    participants, unknown, digest = reconcile(comments, policy, datetime.now(timezone.utc))
    summary = render(participants, unknown, digest, prs, args.repo)
    if not args.dry_run:
        client.upsert(args.hub, summary, comments)
    print(f"Session snapshot: {len(participants)} active; {len(unknown)} unknown joins; {len(prs)} PRs; SHA256 {digest}")
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        Path(os.environ["GITHUB_STEP_SUMMARY"]).write_text(summary, encoding="utf8")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (ValueError, RuntimeError, OSError) as exc:
        print(f"session reconciliation failed: {exc}", file=sys.stderr)
        sys.exit(1)
