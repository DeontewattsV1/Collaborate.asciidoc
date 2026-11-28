"""Static safety checks for the reusable Collaborate Check action.

The action runs in other people's repositories, often under
pull_request_target, so these tests pin down the properties docs/ACTION.md
promises: no pull request code is executed, inputs are never interpolated into
shell scripts, third-party actions are pinned by full commit SHA, and releases
are only ever drafted from a maintainer-pushed tag.
"""
import pathlib
import re
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
ACTION = (ROOT / 'action.yml').read_text(encoding='utf-8')
RELEASE = (ROOT / '.github/workflows/release.yml').read_text(encoding='utf-8')
INTEGRATION = (ROOT / '.github/workflows/action-integration-test.yml').read_text(encoding='utf-8')
EXAMPLE = (ROOT / 'examples/github-action/coordinate.yml').read_text(encoding='utf-8')
DOC = (ROOT / 'docs/ACTION.md').read_text(encoding='utf-8')


def run_blocks(text):
    """Yield the body of every `run: |` block."""
    for m in re.finditer(r'^(\s*)run: \|\n((?:\1\s+.*\n|\s*\n)+)', text, re.M):
        yield m.group(2)


class ActionSafetyTest(unittest.TestCase):
    def test_composite_action_with_documented_outputs(self):
        self.assertRegex(ACTION, r'(?m)^\s+using: composite$')
        for out in ('outcome', 'exit-code', 'blocking-conflicts', 'head-sha', 'report'):
            self.assertRegex(ACTION, rf'(?m)^  {out}:$', out)

    def test_no_expressions_inside_shell_scripts(self):
        blocks = list(run_blocks(ACTION))
        self.assertGreaterEqual(len(blocks), 4)
        for body in blocks:
            self.assertNotIn('${{', body, 'pass values through env:, never interpolate into run scripts')

    def test_never_checks_out_or_runs_pr_code(self):
        self.assertNotIn('actions/checkout', ACTION)
        self.assertNotRegex(ACTION, r'pull_request\.head\.(ref|repo)|github\.head_ref|refs/pull/')
        # The only build is of the action's own source directory.
        self.assertIn('working-directory: ${{ github.action_path }}', ACTION)
        self.assertIn('./cmd/collaborate-check', ACTION)

    def test_third_party_actions_pinned_by_full_sha(self):
        for name, text in (('action.yml', ACTION), ('release.yml', RELEASE), ('integration', INTEGRATION)):
            for ref in re.findall(r'uses:\s*([^\s#]+)', text):
                if ref.startswith('./'):
                    continue
                with self.subTest(file=name, ref=ref):
                    self.assertRegex(ref, r'@[0-9a-f]{40}$')

    def test_release_download_is_checksum_verified(self):
        self.assertIn("--proto '=https'", ACTION)
        self.assertIn('checksums.txt', ACTION)
        self.assertIn('does not match the pinned checksum', ACTION)
        self.assertIn('release mode needs a pinned checksum or verify-attestation: true', ACTION)

    def test_release_workflow_only_drafts_from_tags(self):
        self.assertRegex(RELEASE, r'(?m)^  push:\n    tags:\n      - "v\[0-9\]\+')
        self.assertNotRegex(RELEASE, r'(?m)^  push:\n    branches')
        self.assertIn('--draft', RELEASE)
        self.assertRegex(RELEASE, r"(?m)if: github.ref_type == 'tag'\n\s+env:\n(?:.*\n){0,4}\s+run: \|\n\s+set -euo pipefail\n\s+gh release create")
        self.assertNotRegex(RELEASE, r'git (tag|push)')
        self.assertRegex(RELEASE, r'(?m)^permissions:\n  contents: read$')
        for target in ('linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64', 'windows/amd64'):
            self.assertIn(target, RELEASE)

    def test_integration_test_is_unprivileged(self):
        self.assertNotIn('pull_request_target', INTEGRATION)
        self.assertRegex(INTEGRATION, r'(?m)^permissions:\n  contents: read\n  pull-requests: read$')
        self.assertIn('uses: ./', INTEGRATION)

    def test_example_and_docs_pin_the_published_release_by_full_sha(self):
        pin = re.compile(r'DeontewattsV1/Collaborate\.asciidoc@[0-9a-f]{40} # v\d+\.\d+\.\d+')
        self.assertRegex(EXAMPLE, pin)
        self.assertNotIn('Collaborate.asciidoc@v1', EXAMPLE)
        self.assertNotIn('FULL_COMMIT_SHA', EXAMPLE)
        self.assertNotIn('actions/checkout', EXAMPLE)
        self.assertRegex(DOC, pin)
        self.assertRegex(DOC, r'(?i)status: published')


if __name__ == '__main__':
    unittest.main()
