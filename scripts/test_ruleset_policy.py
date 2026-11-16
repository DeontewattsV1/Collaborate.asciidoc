"""Check the checked-in governance policy; it does not verify live GitHub state."""
import json
import pathlib
import unittest


POLICY = pathlib.Path(__file__).resolve().parents[1] / '.github' / 'rulesets' / 'main-required-ci.json'


class GovernancePolicyTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.policy = json.loads(POLICY.read_text(encoding='utf-8'))
        cls.rules = {rule['type']: rule for rule in cls.policy['rules']}

    def test_active_default_branch_without_bypass(self):
        self.assertEqual(self.policy['enforcement'], 'active')
        self.assertEqual(self.policy['target'], 'branch')
        self.assertIn('~DEFAULT_BRANCH', self.policy['conditions']['ref_name']['include'])
        self.assertEqual(self.policy['bypass_actors'], [])

    def test_independent_review_required(self):
        p = self.rules['pull_request']['parameters']
        self.assertGreaterEqual(p['required_approving_review_count'], 1)
        self.assertTrue(p['dismiss_stale_reviews_on_push'])
        self.assertTrue(p['require_last_push_approval'])

    def test_trusted_source_required_ci_preserved(self):
        p = self.rules['required_status_checks']['parameters']
        self.assertTrue(p['strict_required_status_checks_policy'])
        self.assertIn({'context': 'go-verify', 'integration_id': 15368},
                      p['required_status_checks'])

    def test_verified_signatures_required(self):
        self.assertIn('required_signatures', self.rules)


if __name__ == '__main__':
    unittest.main()
