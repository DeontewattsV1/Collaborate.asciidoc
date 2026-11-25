#!/usr/bin/env python3
"""Crop 2x5 chibi lineup PNGs (1536x1024) into 256x256 square WebP avatars (<40KB).

Usage:
  python3 scripts/crop_lineups.py <input-dir-with-lineups> <labels-manifest.json>
"""
from __future__ import annotations

import json
import sys
from pathlib import Path
from PIL import Image


def crop_grid(src_path: Path, labels_2x5: list[list[str]], out_dir: Path, size: int = 256) -> int:
    im = Image.open(src_path).convert("RGBA")
    w, h = im.size
    col_w = w / 5.0
    row_h = h / 2.0
    count = 0
    for r in range(2):
        for c in range(5):
            cid = labels_2x5[r][c]
            if not cid:
                continue
            x0 = int(round(c * col_w))
            x1 = int(round((c + 1) * col_w))
            y0 = int(round(r * row_h))
            # Trim bottom ~14% label banner so avatar focuses on character art
            y1 = int(round((r + 0.86) * row_h))
            tile = im.crop((x0, y0, x1, y1))
            side = min(tile.size)
            left = (tile.width - side) // 2
            top = max(0, (tile.height - side) // 2 - int(side * 0.04))
            sq = tile.crop((left, top, left + side, top + side)).resize((size, size), Image.LANCZOS)
            sq.save(out_dir / f"{cid}.webp", format="WEBP", quality=82, method=6)
            count += 1
    return count


def main() -> int:
    if len(sys.argv) != 3:
        print("usage: crop_lineups.py <lineup-dir> <manifest.json>", file=sys.stderr)
        return 2
    lineup_dir = Path(sys.argv[1])
    manifest = json.loads(Path(sys.argv[2]).read_text(encoding="utf-8"))
    out_dir = Path(__file__).resolve().parent.parent / "web" / "dev-guild" / "public" / "avatars"
    out_dir.mkdir(parents=True, exist_ok=True)
    total = 0
    for filename, grid in manifest.items():
        src = lineup_dir / filename
        if src.exists():
            total += crop_grid(src, grid, out_dir)
    print(f"Cropped {total} avatars into {out_dir}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
