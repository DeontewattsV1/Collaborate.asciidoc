"""The public playground must stay isolated: static, synthetic, offline.

It is served from GitHub Pages with no login, so it must never reach the
network (and therefore never the private workspace, the secret vault, or
durable rooms), never use storage, and never run an AI model.
"""
import pathlib
import re
import shutil
import subprocess
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
HTML = (ROOT / 'site/playground.html').read_text(encoding='utf-8')
JS_SOURCE = (ROOT / 'site/playground.js').read_text(encoding='utf-8')
# Code only: the header comment names the APIs it promises not to use.
JS = re.sub(r'/\*.*?\*/', '', JS_SOURCE, flags=re.S)
JS = re.sub(r'(?m)^\s*//.*$', '', JS)
PAGES = (ROOT / '.github/workflows/jekyll-gh-pages.yml').read_text(encoding='utf-8')


class PlaygroundIsolationTest(unittest.TestCase):
    def test_csp_blocks_all_network_access(self):
        csp = re.search(r'http-equiv="Content-Security-Policy" content="([^"]+)"', HTML)
        self.assertIsNotNone(csp)
        policy = csp.group(1)
        for directive in ("default-src 'none'", "connect-src 'none'", "script-src 'self'", "form-action 'none'"):
            self.assertIn(directive, policy)
        self.assertNotIn('unsafe-eval', policy)

    def test_no_network_storage_or_dynamic_code(self):
        for api in ('fetch(', 'XMLHttpRequest', 'WebSocket', 'EventSource', 'sendBeacon', 'localStorage',
                    'sessionStorage', 'indexedDB', 'document.cookie', 'innerHTML', 'eval(', 'new Function'):
            self.assertFalse(api in JS, f'playground.js uses {api}')

    def test_no_inline_scripts_or_live_app_calls(self):
        self.assertEqual(re.findall(r'<script(?![^>]*\bsrc=)', HTML), [])
        self.assertNotIn('fly.dev', HTML + JS)
        self.assertNotIn('/api/', HTML + JS)
        self.assertNotIn('<form', HTML)

    def test_labeled_synthetic(self):
        self.assertIn('Synthetic playground', HTML)
        self.assertIn('fictional', HTML)

    def test_published_by_pages_workflow(self):
        self.assertIn('site/playground.html site/playground.js _site/', PAGES)

    @unittest.skipUnless(shutil.which('node'), 'node not installed')
    def test_scenario_engine(self):
        result = subprocess.run(['node', '--test', str(ROOT / 'tests/playground.test.mjs')],
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == '__main__':
    unittest.main()
