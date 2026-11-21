"""Static trust checks for privileged GitHub Actions workflows.

pull_request_target and issue_comment workflows run with the base repository's
token. They must check out trusted base-branch code, never the PR head, and
must not persist checkout credentials. The checkout ref must also be the
current base tip rather than the PR's recorded base commit, which can predate
the workflow and the code it runs.
"""
import pathlib
import re
import unittest

WORKFLOWS = pathlib.Path(__file__).resolve().parents[1] / '.github' / 'workflows'
PRIVILEGED_TRIGGERS = ('pull_request_target', 'issue_comment', 'pull_request_review_comment')
TRUSTED_REFS = {'${{ github.sha }}', '${{ github.event.repository.default_branch }}'}
UNTRUSTED_REF = re.compile(r'pull_request\.head|github\.head_ref|refs/pull/', re.I)


def privileged_workflows():
    for path in sorted(WORKFLOWS.glob('*.y*ml')):
        text = path.read_text(encoding='utf-8')
        on_block = re.search(r'^on:\s*\n((?:[ \t]+.*\n|\s*\n)+)', text, re.M)
        triggers = on_block.group(1) if on_block else ''
        if any(re.search(rf'^\s+{t}:', triggers, re.M) for t in PRIVILEGED_TRIGGERS):
            yield path, text


def checkout_steps(text):
    """Yield the `with:` body of every actions/checkout step."""
    for match in re.finditer(r'uses:\s*actions/checkout@\S+[^\n]*\n((?:[ \t]+(?!-)\S.*\n)*)', text):
        yield match.group(1)


class PrivilegedWorkflowTrustTest(unittest.TestCase):
    def test_privileged_workflows_exist(self):
        names = [p.name for p, _ in privileged_workflows()]
        self.assertIn('agent-registry.yml', names)
        self.assertIn('collaborate-coordination.yml', names)

    def test_checkout_uses_trusted_current_base_ref(self):
        for path, text in privileged_workflows():
            steps = list(checkout_steps(text))
            self.assertTrue(steps, f'{path.name}: expected an actions/checkout step')
            for body in steps:
                with self.subTest(workflow=path.name):
                    ref = re.search(r'^\s+ref:\s*(.+?)\s*$', body, re.M)
                    self.assertIsNotNone(ref, f'{path.name}: checkout must pin an explicit trusted ref')
                    self.assertNotRegex(ref.group(1), UNTRUSTED_REF, f'{path.name}: checks out PR head code')
                    self.assertIn(ref.group(1), TRUSTED_REFS,
                                  f'{path.name}: ref {ref.group(1)} is not the current trusted base tip '
                                  '(pull_request.base.sha can predate the workflow)')
                    self.assertRegex(body, r'(?m)^\s+persist-credentials:\s*false\s*$',
                                     f'{path.name}: checkout must not persist credentials')


if __name__ == '__main__':
    unittest.main()
