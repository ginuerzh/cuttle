#!/usr/bin/env python3
"""Generate internal/tokenest/testdata/calibration.jsonl.

Provenance tool only; it needs the official DeepSeek V4 tokenizer and the
`tokenizers` Python package. It never runs in CI.
"""
import argparse
import json
import random
import re
from pathlib import Path

from tokenizers import Tokenizer

ROOT = Path(__file__).resolve().parents[1]
FILES = [
    "README.md",
    "DOCKER.md",
    "internal/gateway/build.go",
    "internal/gateway/upstream.go",
    "internal/rewriter/rewriter.go",
    "../llm-api-converter/README.zh-CN.md",
    "../llm-api-converter/README.md",
]
TOOL_NAMES = ["get_weather", "search_web", "read_file", "run_command", "create_issue", "send_email"]
EMOJI = ["😀🎉🚀👨‍👩‍👧‍👦 表情符号测试。", "Mixed emoji 😀 and 中文 and English.", "🇨🇳🇺🇸🏳️‍🌈 flags and 👍🏽 skin tones"]


def chunks():
    out = []
    for rel in FILES:
        text = re.sub(r"\n{3,}", "\n\n", (ROOT / rel).read_text(encoding="utf-8"))
        for i in range(0, len(text), 700):
            chunk = text[i:i + 700]
            if len(chunk) >= 120:
                out.append(chunk)

    rnd = random.Random(7)
    for name in TOOL_NAMES:
        props = {
            f"param_{j}": {
                "type": ["string", "number", "boolean"][j % 3],
                "description": f"Parameter {j} for {name}",
            }
            for j in range(rnd.randint(2, 8))
        }
        out.append(json.dumps({
            "name": name,
            "description": f"Tool {name} used by the agent to perform an operation",
            "input_schema": {"type": "object", "properties": props, "required": list(props)[:2]},
        }, ensure_ascii=False))
    out.extend(EMOJI)
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--tokenizer", required=True)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()

    tok = Tokenizer.from_file(args.tokenizer)
    rows = chunks()
    with open(args.out, "w", encoding="utf-8") as f:
        for text in rows:
            f.write(json.dumps({"text": text, "tokens": len(tok.encode(text).ids)}, ensure_ascii=False) + "\n")
    print(f"wrote {len(rows)} rows to {args.out}")


if __name__ == "__main__":
    main()
