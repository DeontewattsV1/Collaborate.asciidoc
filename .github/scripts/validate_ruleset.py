#!/usr/bin/env python3
"""Validate the repository's checked-in default-branch ruleset proposal."""

import json
from pathlib import Path
import sys

RULESET_PATH = Path(__file__).resolve().parents[1] / "rulesets" / "main-required-ci.json"
EXPECTED_CHECKS = {"go-verify", "agent-registry"}
EXPECTED_INTEGRATION = 15368  # GitHub Actions app integration ID


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
    if config.get("bypass_actors") != []:
        fail("ruleset must not define bypass actors")

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
    if not isinstance(pr_parameters, dict) or pr_parameters.get("required_approving_review_count") != 0:
        fail("ruleset approval count differs from the reviewed proposal")

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

    print("Ruleset valid: active default-branch PR rule requires strict go-verify and agent-registry from GitHub Actions; no bypass actors.")


if __name__ == "__main__":
    try:
        validate()
    except ValueError as exc:
        print(f"ruleset validation failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
