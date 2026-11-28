"""Exercise .github/scripts/validate_ruleset.py end to end, including its success path."""
import contextlib
import copy
import importlib.util
import io
import json
import pathlib
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
SCRIPT = ROOT / '.github' / 'scripts' / 'validate_ruleset.py'


def load_validator():
    spec = importlib.util.spec_from_file_location('validate_ruleset', SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class ValidateRulesetTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.v = load_validator()
        cls.policy = json.loads(cls.v.RULESET_PATH.read_text(encoding='utf-8'))

    def run_validate(self, config):
        with tempfile.TemporaryDirectory() as d:
            path = pathlib.Path(d) / 'ruleset.json'
            path.write_text(json.dumps(config), encoding='utf-8')
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                self.v.validate(path)
            return out.getvalue()

    def test_checked_in_ruleset_passes_and_names_every_required_check(self):
        # Regression: the success message once referenced an undefined constant,
        # so a valid ruleset crashed with NameError after all checks passed.
        message = self.run_validate(self.policy)
        self.assertIn('Ruleset valid', message)
        for check in self.v.EXPECTED_CHECKS:
            self.assertIn(check, message)

    def test_weakened_rulesets_are_rejected(self):
        def pr(c):
            return next(r for r in c['rules'] if r['type'] == 'pull_request')['parameters']

        def status(c):
            return next(r for r in c['rules'] if r['type'] == 'required_status_checks')['parameters']

        cases = {
            'zero approvals': lambda c: pr(c).update(required_approving_review_count=0),
            'boolean approvals': lambda c: pr(c).update(required_approving_review_count=True),
            'stale approvals kept': lambda c: pr(c).update(dismiss_stale_reviews_on_push=False),
            'last push unapproved': lambda c: pr(c).pop('require_last_push_approval'),
            'bypass actor': lambda c: c.update(bypass_actors=[{'actor_id': 1}]),
            'admin always bypass': lambda c: c.update(bypass_actors=[
                {'actor_id': 5, 'actor_type': 'RepositoryRole', 'bypass_mode': 'always'}]),
            'integration bypass': lambda c: c.update(bypass_actors=[
                {'actor_id': 5, 'actor_type': 'Integration', 'bypass_mode': 'pull_request'}]),
            'loose status policy': lambda c: status(c).update(strict_required_status_checks_policy=False),
        }
        # Dropping any one required check must fail, so a check added to
        # EXPECTED_CHECKS later is covered automatically.
        for check in sorted(self.v.EXPECTED_CHECKS):
            cases[f'{check} dropped'] = lambda c, check=check: status(c).update(required_status_checks=[
                s for s in status(c)['required_status_checks'] if s.get('context') != check])
        for name, mutate in cases.items():
            with self.subTest(name):
                config = copy.deepcopy(self.policy)
                mutate(config)
                with self.assertRaises(ValueError):
                    self.run_validate(config)


    def test_live_check_is_skipped_without_a_token(self):
        msg = self.v.validate_live(self.policy, env={}, get=lambda *a: self.fail('no API call without a token'))
        self.assertIn('skipped', msg)

    def live(self, **overrides):
        live = copy.deepcopy(self.policy)
        live.update(id=1, **overrides)
        calls = []

        def get(url, token):
            calls.append(url)
            return [{'id': 1, 'name': self.policy['name']}] if url.endswith('/rulesets') else live
        return get, calls

    def test_live_check_passes_for_a_matching_active_ruleset(self):
        get, calls = self.live()
        env = {'GITHUB_TOKEN': 't', 'GITHUB_REPOSITORY': 'o/r'}
        self.assertIn('matches', self.v.validate_live(self.policy, env=env, get=get))
        self.assertEqual(calls[0], 'https://api.github.com/repos/o/r/rulesets')

    def test_live_check_fails_for_a_disabled_or_weaker_ruleset(self):
        env = {'GITHUB_TOKEN': 't', 'GITHUB_REPOSITORY': 'o/r'}
        for name, overrides in {
            'disabled': {'enforcement': 'disabled'},
            'extra bypass': {'bypass_actors': [{'actor_id': 1, 'actor_type': 'Team', 'bypass_mode': 'always'}]},
            'no rules': {'rules': []},
            'other branch': {'conditions': {'ref_name': {'include': ['refs/heads/other'], 'exclude': []}}},
            'zero approvals': {'rules': [
                {**r, 'parameters': {**r['parameters'], 'required_approving_review_count': 0}} if r['type'] == 'pull_request' else r
                for r in self.policy['rules']]},
            'loose checks': {'rules': [
                {**r, 'parameters': {**r['parameters'], 'strict_required_status_checks_policy': False}}
                if r['type'] == 'required_status_checks' else r for r in self.policy['rules']]},
        }.items():
            with self.subTest(name):
                get, _ = self.live(**overrides)
                with self.assertRaises(ValueError):
                    self.v.validate_live(self.policy, env=env, get=get)


if __name__ == '__main__':
    unittest.main()
