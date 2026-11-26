"""Exercise the planner in .github/scripts/ghcr_cleanup.py without network access."""
import importlib.util
import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
SCRIPT = ROOT / '.github' / 'scripts' / 'ghcr_cleanup.py'


def load():
    spec = importlib.util.spec_from_file_location('ghcr_cleanup', SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def digest(n, kind):
    return 'sha256:' + format(n * 10 + kind, '064x')


INDEX = 'application/vnd.oci.image.index.v1+json'
MANIFEST = 'application/vnd.oci.image.manifest.v1+json'


class Registry:
    """Build a fake package the way build-push-action + cosign publish it."""

    def __init__(self):
        self.versions, self.manifests, self.next_id = [], {}, 1

    def _add(self, d, created, tags, manifest):
        self.versions.append({'id': self.next_id, 'name': d, 'created_at': created, 'tags': list(tags)})
        self.manifests[d] = manifest
        self.next_id += 1

    def image(self, n, tags=(), signed=True):
        created = f'2026-11-{n:02d}T00:00:00Z'
        index, amd64, prov = digest(n, 1), digest(n, 2), digest(n, 3)
        self._add(amd64, created, [], {'mediaType': MANIFEST,
                                       'layers': [{'mediaType': 'application/vnd.oci.image.layer.v1.tar+gzip'}]})
        self._add(prov, created, [], {'mediaType': MANIFEST,
                                      'layers': [{'mediaType': 'application/vnd.in-toto+json'}]})
        self._add(index, created, tags, {'mediaType': INDEX, 'manifests': [{'digest': amd64}, {'digest': prov}]})
        if signed:
            self.signature(index, created)
        return index

    def signature(self, target, created, tagged=True, n=None):
        sig = 'sha256:' + format(int(target[7:], 16) + 7 + (n or 0) * 1000, '064x')
        tags = ['sha256-' + target[7:] + '.sig'] if tagged else []
        self._add(sig, created, tags, {'mediaType': MANIFEST,
                                       'config': {'mediaType': 'application/vnd.oci.image.config.v1+json'},
                                       'layers': [{'mediaType': 'application/vnd.dev.cosign.simplesigning.v1+json'}]})
        return sig


class GhcrCleanupPlanTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.m = load()

    def run_plan(self, reg, keep=10):
        keep_ids, delete, kept_images = self.m.plan(reg.versions, reg.manifests, keep)
        deleted = {v['name'] for v, _ in delete}
        kept = {v['name'] for v in reg.versions if v['id'] in keep_ids}
        self.assertEqual(deleted | kept, {v['name'] for v in reg.versions})
        self.assertFalse(deleted & kept)
        return kept, deleted, kept_images

    def test_keeps_newest_ten_images_with_children_and_signatures(self):
        reg = Registry()
        indexes = [reg.image(n, tags=['main', 'latest'] if n == 15 else ()) for n in range(1, 16)]
        kept, deleted, kept_images = self.run_plan(reg)
        self.assertEqual(kept_images, list(reversed(indexes))[:10])
        for n, index in enumerate(indexes, start=1):
            group = {index, digest(n, 2), digest(n, 3), 'sha256:' + format(int(index[7:], 16) + 7, '064x')}
            target = kept if n > 5 else deleted
            self.assertTrue(group <= target, f'image {n} should be wholly {"kept" if n > 5 else "deleted"}')

    def test_release_and_moving_tags_are_always_kept(self):
        reg = Registry()
        release = reg.image(1, tags=['v1.0.0'])
        nightly = reg.image(2, tags=['nightly'])
        for n in range(3, 20):
            reg.image(n)
        kept, deleted, _ = self.run_plan(reg, keep=3)
        for index, n in ((release, 1), (nightly, 2)):
            self.assertIn(index, kept)
            self.assertIn(digest(n, 2), kept, 'child of a kept tagged image was deleted')

    def test_shared_child_of_a_kept_image_is_never_deleted(self):
        reg = Registry()
        old = reg.image(1)
        new = reg.image(2)
        reg.manifests[new]['manifests'].append({'digest': digest(1, 2)})
        kept, deleted, _ = self.run_plan(reg, keep=1)
        self.assertIn(old, deleted)
        self.assertIn(digest(1, 2), kept)

    def test_orphan_and_superseded_signatures_are_removed(self):
        reg = Registry()
        live = reg.image(1)
        superseded = reg.signature(live, '2026-11-01T00:00:00Z', tagged=False, n=1)
        orphan = reg.signature('sha256:' + 'f' * 64, '2026-10-01T00:00:00Z')
        kept, deleted, _ = self.run_plan(reg)
        self.assertIn(superseded, deleted)
        self.assertIn(orphan, deleted)
        self.assertIn(live, kept)

    def test_signatures_never_count_toward_the_keep_limit(self):
        reg = Registry()
        for n in range(1, 4):
            reg.image(n)
        _, deleted, kept_images = self.run_plan(reg, keep=3)
        self.assertEqual(len(kept_images), 3)
        self.assertEqual(deleted, set())

    def test_fails_closed_when_a_manifest_is_unreadable(self):
        reg = Registry()
        reg.image(1)
        reg.manifests[reg.versions[0]['name']] = None
        with self.assertRaises(RuntimeError):
            self.m.plan(reg.versions, reg.manifests, 10)

    def test_rejects_keep_below_one(self):
        with self.assertRaises(ValueError):
            self.m.plan([], {}, 0)


if __name__ == '__main__':
    unittest.main()
