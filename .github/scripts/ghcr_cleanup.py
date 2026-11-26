#!/usr/bin/env python3
"""Prune old versions of this repository's GHCR container package.

A published image is more than one package version, so "keep the newest N
versions" is not safe on its own:

  * docker/build-push-action pushes an image *index* plus untagged child
    manifests (one per platform, plus buildx provenance attestations).
    Deleting a child of an image we keep breaks pulls of that image.
  * cosign stores each signature as a separate version tagged
    ``sha256-<image digest>.sig``. Deleting it while keeping the image makes
    ``cosign verify`` fail; keeping it after the image is gone leaves an orphan.
  * Moving tags such as ``main`` leave older builds untagged, so "untagged"
    does not mean "garbage" either.

This script therefore reads every manifest, groups versions into images, keeps
the newest ``--keep`` images plus every image that still carries a tag (for
example ``main``, ``latest``, ``nightly`` or a ``v1.2.3`` release), and deletes
each other image together with its children and signatures. It fails closed:
if any manifest cannot be read, nothing is deleted.

Deletion is permanent. Run with ``--dry-run`` (the default in the workflow
unless cleanup is explicitly enabled) to see the plan first.

Usage:
  GITHUB_TOKEN=... python3 ghcr_cleanup.py --owner OWNER --package NAME [--keep 10] [--dry-run]
"""
import argparse
import base64
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request

SIG_TAG = re.compile(r'^sha256-([0-9a-f]{64})\.(sig|att|sbom)$')
INDEX_TYPES = {
    'application/vnd.oci.image.index.v1+json',
    'application/vnd.docker.distribution.manifest.list.v2+json',
}
MANIFEST_ACCEPT = ', '.join([
    'application/vnd.oci.image.index.v1+json',
    'application/vnd.docker.distribution.manifest.list.v2+json',
    'application/vnd.oci.image.manifest.v1+json',
    'application/vnd.docker.distribution.manifest.v2+json',
])
ARTIFACT_LAYER_PREFIXES = (
    'application/vnd.dev.cosign',  # cosign simple-signing payloads
    'application/vnd.dsse',  # cosign / in-toto DSSE envelopes
    'application/vnd.in-toto',  # buildx provenance and SBOM attestations
)


# --------------------------------------------------------------------------
# Planning (pure; unit-tested in scripts/test_ghcr_cleanup.py)
# --------------------------------------------------------------------------

def children_of(manifest):
    if not manifest:
        return []
    if manifest.get('mediaType') in INDEX_TYPES or 'manifests' in manifest:
        return [m['digest'] for m in manifest.get('manifests', []) if m.get('digest')]
    return []


def is_artifact(manifest):
    """True for signature/attestation manifests rather than runnable images."""
    if not manifest or 'manifests' in manifest:
        return False
    layers = manifest.get('layers') or []
    config_type = (manifest.get('config') or {}).get('mediaType', '')
    if 'cosign' in config_type:
        return True
    return bool(layers) and all(
        str(layer.get('mediaType', '')).startswith(ARTIFACT_LAYER_PREFIXES) for layer in layers
    )


def artifact_target(version, manifest):
    """Digest of the image a signature/attestation belongs to, if known."""
    for tag in version.get('tags') or []:
        match = SIG_TAG.match(tag)
        if match:
            return 'sha256:' + match.group(1)
    subject = (manifest or {}).get('subject') or {}
    return subject.get('digest')


def plan(versions, manifests, keep):
    """Return (keep_ids, delete_list, kept_image_digests).

    delete_list holds (version, reason) pairs, oldest first.

    versions:  [{'id', 'name' (digest), 'created_at', 'tags': [...]}, ...]
    manifests: {digest: parsed manifest JSON}; every version must be present.
    """
    if keep < 1:
        raise ValueError('keep must be at least 1')
    missing = [v['name'] for v in versions if manifests.get(v['name']) is None]
    if missing:
        raise RuntimeError(f'refusing to plan: {len(missing)} manifest(s) unreadable, e.g. {missing[0]}')

    by_digest = {v['name']: v for v in versions}
    parents = {}
    for v in versions:
        for child in children_of(manifests[v['name']]):
            parents.setdefault(child, set()).add(v['name'])

    def is_sig_version(v):
        return any(SIG_TAG.match(t) for t in v.get('tags') or []) or is_artifact(manifests[v['name']])

    artifacts = [v for v in versions if is_sig_version(v) and v['name'] not in parents]
    images = [v for v in versions if v['name'] not in parents and not is_sig_version(v)]
    images.sort(key=lambda v: v.get('created_at') or '', reverse=True)

    kept_images = []
    for index, image in enumerate(images):
        if index < keep or image.get('tags'):
            kept_images.append(image)
    kept_image_digests = {v['name'] for v in kept_images}

    def closure(digests):
        seen, stack = set(), list(digests)
        while stack:
            d = stack.pop()
            if d in seen:
                continue
            seen.add(d)
            stack.extend(children_of(manifests.get(d)))
        return seen

    keep_digests = closure(kept_image_digests)
    deleted_images = [v for v in images if v['name'] not in kept_image_digests]
    delete = {}
    for image in deleted_images:
        delete[image['name']] = (image, 'image older than the newest %d and untagged' % keep)
        for child in closure([image['name']]) - {image['name']}:
            if child in by_digest and child not in keep_digests:
                delete[child] = (by_digest[child], f'child manifest of deleted image {image["name"][:19]}')

    for art in artifacts:
        target = artifact_target(art, manifests[art['name']])
        if target and target in keep_digests:
            if art.get('tags'):
                keep_digests.add(art['name'])
                continue
            # An untagged signature for a kept image is a superseded copy:
            # cosign rewrites the tag when it appends a new signature.
            delete[art['name']] = (art, f'superseded signature of kept image {target[:19]}')
        elif target and target in delete:
            delete[art['name']] = (art, f'signature of deleted image {target[:19]}')
        elif target and target not in by_digest:
            delete[art['name']] = (art, f'orphaned signature of missing image {target[:19]}')
        elif not target:
            delete[art['name']] = (art, 'orphaned signature/attestation with no target')
        else:
            keep_digests.add(art['name'])

    # Untagged children whose parents are all gone already are orphans too.
    for v in versions:
        d = v['name']
        if d in parents and d not in keep_digests and d not in delete:
            if all(p in delete for p in parents[d]):
                delete[d] = (v, 'child manifest of deleted image')
            else:
                keep_digests.add(d)

    # Never delete anything that is still tagged with a non-signature tag.
    for d, (v, _) in list(delete.items()):
        if any(not SIG_TAG.match(t) for t in v.get('tags') or []):
            del delete[d]

    keep_ids = sorted(v['id'] for v in versions if v['name'] not in delete)
    delete_list = sorted(delete.values(), key=lambda item: item[0].get('created_at') or '')
    return keep_ids, delete_list, [v['name'] for v in kept_images]


# --------------------------------------------------------------------------
# GitHub Packages and GHCR I/O
# --------------------------------------------------------------------------

def _request(url, token=None, method='GET', accept=None, basic=None):
    req = urllib.request.Request(url, method=method)
    if basic:
        req.add_header('Authorization', 'Basic ' + base64.b64encode(basic.encode()).decode())
    elif token:
        req.add_header('Authorization', f'Bearer {token}')
    if accept:
        req.add_header('Accept', accept)
    req.add_header('User-Agent', 'ghcr-cleanup')
    with urllib.request.urlopen(req, timeout=30) as resp:
        body = resp.read()
        return resp.status, (json.loads(body) if body else None)


class GitHub:
    def __init__(self, token, owner, package, api='https://api.github.com'):
        package = package.lower()
        self.token, self.owner, self.package, self.api = token, owner, package, api.rstrip('/')
        _, user = self._call(f'/users/{urllib.parse.quote(owner)}')
        scope = 'orgs' if user.get('type') == 'Organization' else 'users'
        self.base = f'/{scope}/{urllib.parse.quote(owner)}/packages/container/{urllib.parse.quote(package, safe="")}'

    def _call(self, path, method='GET'):
        return _request(self.api + path, self.token, method, 'application/vnd.github+json')

    def versions(self):
        out, page = [], 1
        while True:
            _, batch = self._call(f'{self.base}/versions?per_page=100&page={page}')
            if not batch:
                return out
            for v in batch:
                out.append({
                    'id': v['id'],
                    'name': v['name'],
                    'created_at': v.get('created_at'),
                    'tags': ((v.get('metadata') or {}).get('container') or {}).get('tags') or [],
                })
            page += 1

    def delete(self, version_id):
        self._call(f'{self.base}/versions/{version_id}', method='DELETE')


def fetch_manifests(token, owner, package, digests, registry='ghcr.io'):
    image = f'{owner}/{package}'.lower()
    scope = urllib.parse.quote(f'repository:{image}:pull')
    _, auth = _request(f'https://{registry}/token?service={registry}&scope={scope}', basic=f'x:{token}')
    bearer = auth['token']
    manifests = {}
    for digest in digests:
        try:
            _, manifests[digest] = _request(
                f'https://{registry}/v2/{image}/manifests/{digest}', bearer, accept=MANIFEST_ACCEPT)
        except urllib.error.HTTPError as err:
            print(f'warning: cannot read manifest {digest}: HTTP {err.code}', file=sys.stderr)
            manifests[digest] = None
    return manifests


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split('\n\n')[0])
    ap.add_argument('--owner', required=True)
    ap.add_argument('--package', required=True)
    ap.add_argument('--keep', type=int, default=10, help='newest images to keep (tagged images are always kept)')
    ap.add_argument('--max-delete', type=int, default=500, help='abort if the plan deletes more than this')
    ap.add_argument('--dry-run', action='store_true')
    args = ap.parse_args(argv)
    token = os.environ.get('GITHUB_TOKEN', '')
    if not token:
        print('GITHUB_TOKEN is required', file=sys.stderr)
        return 2

    gh = GitHub(token, args.owner, args.package)
    versions = gh.versions()
    manifests = fetch_manifests(token, args.owner, args.package, [v['name'] for v in versions])
    try:
        keep_ids, delete, kept_images = plan(versions, manifests, args.keep)
    except RuntimeError as err:
        print(f'error: {err}. Nothing was deleted.', file=sys.stderr)
        return 1

    print(f'{len(versions)} package versions; keeping {len(keep_ids)} '
          f'({len(kept_images)} images), deleting {len(delete)}.')
    for digest in kept_images:
        tags = ', '.join(next(v for v in versions if v['name'] == digest)['tags']) or 'untagged'
        print(f'  keep image  {digest[:19]}  [{tags}]')
    for version, reason in delete:
        print(f'  delete {version["id"]:>12}  {version["name"][:19]}  {version.get("created_at", "")}  {reason}')

    if len(delete) > args.max_delete:
        print(f'error: plan deletes {len(delete)} > --max-delete {args.max_delete}. Nothing was deleted.',
              file=sys.stderr)
        return 1
    if args.dry_run:
        print('Dry run: nothing was deleted.')
        return 0
    failures = 0
    for version, _ in delete:
        try:
            gh.delete(version['id'])
        except urllib.error.HTTPError as err:
            failures += 1
            print(f'error: delete {version["id"]} failed: HTTP {err.code}', file=sys.stderr)
    print(f'Deleted {len(delete) - failures} of {len(delete)} versions.')
    return 1 if failures else 0


if __name__ == '__main__':
    sys.exit(main())
