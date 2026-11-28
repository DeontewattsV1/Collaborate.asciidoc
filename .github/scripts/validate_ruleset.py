#!/usr/bin/env python3
"""Validate the repository's default-branch ruleset.

Always validates the checked-in policy. When GITHUB_TOKEN or GH_TOKEN is set
(and the repository is known from GITHUB_REPOSITORY or the origin remote), it
also reads the live ruleset through the GitHub API and fails if it is missing,
not active, or weaker than the checked-in policy. Without a token the live
check is skipped with a note, so forks and offline runs still pass.
"""

import json
import os
from pathlib import Path
import re
import subprocess
import sys
import urllib.error
import urllib.request

RULESET_PATH = Path(__file__).resolve().parents[1] / "rulesets" / "main-required-ci.json"
EXPECTED_CHECKS = {"go-verify", "agent-registry"}
EXPECTED_INTEGRATION = 15368  # GitHub Actions app integration ID
# The only bypass allowed: repository admins (role id 5), and only when
# merging a pull request (never a direct push). This keeps the sole
# maintainer from being locked out of merging their own PRs.
ALLOWED_BYPASS = {"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "pull_request"}


def fail(message: str) -> None:
    raise ValueError(message)


def validate(path: Path = RULESET_PATH) -> None:
    try:
        config = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        fail(f"cannot read valid ruleset JSON at {path}: {exc}")
    if not isinstance(config, dict):
        fail("ruleset root must be a JSON object")

    if config.get("name") != "main-require-independent-ci":
        fail("ruleset name must be main-require-independent-ci")
    if config.get("target") != "branch" or config.get("enforcement") != "active":
        fail("ruleset must be active and target branches")
    bypass = config.get("bypass_actors")
    if not isinstance(bypass, list) or any(
        not isinstance(actor, dict) or {k: actor.get(k) for k in ALLOWED_BYPASS} != ALLOWED_BYPASS for actor in bypass
    ):
        fail("ruleset bypass is limited to repository admins merging through a pull request")

    conditions = config.get("conditions")
    if not isinstance(conditions, dict):
        fail("ruleset conditions must be an object")
    ref_name = conditions.get("ref_name")
    includes = ref_name.get("include") if isinstance(ref_name, dict) else None
    if not isinstance(includes, list) or "~DEFAULT_BRANCH" not in includes:
        fail("ruleset must target the repository default branch")

    rules = config.get("rules")
    if not isinstance(rules, list) or any(not isinstance(rule, dict) for rule in rules):
        fail("rules must be an array of objects")
    pull_request = [rule for rule in rules if rule.get("type") == "pull_request"]
    if len(pull_request) != 1:
        fail("ruleset must contain exactly one pull_request rule")
    pr_parameters = pull_request[0].get("parameters")
    if not isinstance(pr_parameters, dict):
        fail("pull_request parameters must be an object")
    # Mirrors the CI policy described in docs/GITHUB-CI.md and
    # scripts/test_ruleset_policy.py): at least one independent approval that
    # is invalidated by later pushes.
    approvals = pr_parameters.get("required_approving_review_count")
    if not isinstance(approvals, int) or isinstance(approvals, bool) or approvals < 1:
        fail("ruleset must require at least one approving review")
    if pr_parameters.get("dismiss_stale_reviews_on_push") is not True:
        fail("ruleset must dismiss stale approvals on push")
    if pr_parameters.get("require_last_push_approval") is not True:
        fail("ruleset must require approval of the most recent push")

    status_rules = [rule for rule in rules if rule.get("type") == "required_status_checks"]
    if len(status_rules) != 1:
        fail("ruleset must contain exactly one required_status_checks rule")
    parameters = status_rules[0].get("parameters")
    if not isinstance(parameters, dict):
        fail("required_status_checks parameters must be an object")
    if parameters.get("strict_required_status_checks_policy") is not True:
        fail("required status checks must use strict up-to-date enforcement")
    checks = parameters.get("required_status_checks")
    if not isinstance(checks, list):
        fail("required_status_checks must be an array")
    required = {
        check.get("context")
        for check in checks
        if isinstance(check, dict) and check.get("integration_id") == EXPECTED_INTEGRATION
    }
    missing = EXPECTED_CHECKS - required
    if missing:
        fail(f"ruleset must require {', '.join(sorted(missing))} from GitHub Actions ({EXPECTED_INTEGRATION})")

    bypass_note = "admin bypass only via pull request" if bypass else "no bypass actors"
    print(f"Ruleset valid: active default-branch PR rule requires {approvals}+ fresh approval(s) and strict {', '.join(sorted(EXPECTED_CHECKS))} from GitHub Actions; {bypass_note}.")
    return config


def repository_slug() -> str:
    slug = os.environ.get("GITHUB_REPOSITORY", "").strip()
    if slug:
        return slug
    try:
        url = subprocess.run(["git", "remote", "get-url", "origin"], capture_output=True, text=True, check=True).stdout.strip()
    except (OSError, subprocess.CalledProcessError):
        return ""
    m = re.search(r"github\.com[:/]([^/]+/[^/]+?)(?:\.git)?$", url)
    return m.group(1) if m else ""


def github_get(url: str, token: str):
    req = urllib.request.Request(url, headers={
        "Authorization": f"Bearer {token}",
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
        "User-Agent": "collaborate-ruleset-check",
    })
    with urllib.request.urlopen(req, timeout=15) as resp:
        return json.loads(resp.read().decode("utf-8"))


def compare_live(policy: dict, live: dict) -> None:
    """Fail if the live ruleset is weaker than the checked-in policy."""
    if live.get("enforcement") != "active":
        fail(f"live ruleset {policy['name']} is {live.get('enforcement')!r}, not active")
    want_bypass = sorted(json.dumps({k: a.get(k) for k in ALLOWED_BYPASS}, sort_keys=True) for a in policy.get("bypass_actors", []))
    got_bypass = sorted(json.dumps({k: a.get(k) for k in ALLOWED_BYPASS}, sort_keys=True) for a in live.get("bypass_actors", []) or [])
    if got_bypass != want_bypass:
        fail("live ruleset bypass actors differ from the checked-in policy")
    want_types = {r.get("type") for r in policy.get("rules", [])}
    got_types = {r.get("type") for r in live.get("rules", []) or []}
    if want_types - got_types:
        fail(f"live ruleset is missing rules: {', '.join(sorted(want_types - got_types))}")
    live_status = next((r for r in live.get("rules", []) if r.get("type") == "required_status_checks"), {})
    contexts = {c.get("context") for c in (live_status.get("parameters") or {}).get("required_status_checks", [])}
    if EXPECTED_CHECKS - contexts:
        fail(f"live ruleset does not require {', '.join(sorted(EXPECTED_CHECKS - contexts))}")


def validate_live(policy: dict, env=None, get=github_get) -> str:
    env = os.environ if env is None else env
    token = (env.get("GITHUB_TOKEN") or env.get("GH_TOKEN") or "").strip()
    if not token:
        return "live ruleset check skipped (no GITHUB_TOKEN/GH_TOKEN)"
    slug = (env.get("GITHUB_REPOSITORY") or "").strip() or repository_slug()
    if not slug:
        return "live ruleset check skipped (repository unknown)"
    api = (env.get("GITHUB_API_URL") or "https://api.github.com").rstrip("/")
    try:
        rulesets = get(f"{api}/repos/{slug}/rulesets", token)
        match = next((r for r in rulesets if r.get("name") == policy["name"]), None)
        if match is None:
            fail(f"live ruleset {policy['name']} does not exist on {slug}")
        live = get(f"{api}/repos/{slug}/rulesets/{match['id']}", token)
    except (urllib.error.URLError, TimeoutError, OSError) as exc:
        return f"live ruleset check skipped (GitHub API unreachable: {exc.__class__.__name__})"
    compare_live(policy, live)
    return f"Live ruleset {policy['name']} on {slug} is active and matches the checked-in policy."


if __name__ == "__main__":
    try:
        policy = validate()
        print(validate_live(policy))
    except ValueError as exc:
        print(f"ruleset validation failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
