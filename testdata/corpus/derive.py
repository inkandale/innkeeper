#!/usr/bin/env python3
"""Regenerate the derived corpus cards from Piko (CC0). Run from anywhere."""
import json, struct
from pathlib import Path

root = Path(__file__).parent


def v2_only_png():
    data = (root / "piko/source.png").read_bytes()
    sig, pos, out = data[:8], 8, [data[:8]]
    while pos < len(data):
        n, typ = struct.unpack(">I4s", data[pos:pos + 8])
        chunk = data[pos:pos + 12 + n]  # length, type, data, CRC kept verbatim
        if not (typ == b"tEXt" and chunk[8:].startswith(b"ccv3\0")):
            out.append(chunk)
        pos += 12 + n
    dst = root / "piko-v2-only-png/source.png"
    dst.parent.mkdir(exist_ok=True)
    dst.write_bytes(b"".join(out))


def regex_json():
    card = json.loads((root / "piko-json-v3/source.json").read_text())
    # Field names match SillyTavern's regex extension script objects.
    card["data"]["extensions"]["regex_scripts"] = [{
        "id": "6f1c2b0e-3a54-4d1e-9b7a-2e8d5c0a41f3",
        "scriptName": "Emphasis to HTML",
        "findRegex": "/\\*([^*]+)\\*/g",
        "replaceString": "<em>$1</em>",
        "trimStrings": [],
        "placement": [2],
        "disabled": False,
        "markdownOnly": True,
        "promptOnly": False,
        "runOnEdit": True,
        "substituteRegex": 0,
        "minDepth": None,
        "maxDepth": None,
    }]
    dst = root / "piko-regex/source.json"
    dst.parent.mkdir(exist_ok=True)
    dst.write_text(json.dumps(card, indent=2, ensure_ascii=False) + "\n")


v2_only_png()
regex_json()
