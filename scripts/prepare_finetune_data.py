#!/usr/bin/env python3
"""Convert suzuha conversation logs to fine-tuning format for Unsloth.

Usage:
    python scripts/prepare_finetune_data.py [--input data/finetune/raw_logs.jsonl] [--output data/finetune/train.jsonl]

Reads the JSONL exported from GET /api/conversation-logs/export and produces
a cleaned dataset suitable for chat fine-tuning (ChatML / OpenAI format).

Filtering rules:
- Keep only user → assistant text exchanges
- Strip tool calls, tool results, and tool_call_id messages
- If a turn has user + assistant (with tool stuff in between), extract
  the user messages and the final assistant text response
- Skip turns where assistant has no text content
- Skip very short assistant responses (< 2 chars)
"""

import argparse
import json
import sys
from pathlib import Path


def extract_conversation(messages: list[dict]) -> list[dict] | None:
    """Extract user/assistant pairs from a turn, stripping tool interactions."""
    cleaned = []

    for msg in messages:
        role = msg.get("role", "")
        content = msg.get("content", "").strip()

        # Skip tool-related messages entirely
        if role == "tool":
            continue
        if msg.get("tool_call_id"):
            continue

        # For assistant messages with tool_calls, only keep if there's also text
        if role == "assistant":
            if msg.get("tool_calls") and not content:
                continue
            # Strip tool_calls from the output, keep only text
            if content:
                cleaned.append({"role": "assistant", "content": content})
            continue

        if role == "user" and content:
            cleaned.append({"role": "user", "content": content})

    if not cleaned:
        return None

    # Merge consecutive messages of the same role
    merged = [cleaned[0]]
    for msg in cleaned[1:]:
        if msg["role"] == merged[-1]["role"]:
            merged[-1]["content"] += "\n" + msg["content"]
        else:
            merged.append(msg)

    # Must have at least one user → assistant exchange
    has_user = any(m["role"] == "user" for m in merged)
    has_assistant = any(m["role"] == "assistant" for m in merged)
    if not has_user or not has_assistant:
        return None

    # Skip if all assistant responses are very short
    assistant_texts = [m["content"] for m in merged if m["role"] == "assistant"]
    if all(len(t) < 2 for t in assistant_texts):
        return None

    return merged


def main():
    parser = argparse.ArgumentParser(description="Prepare fine-tuning data")
    parser.add_argument("--input", default="data/finetune/raw_logs.jsonl")
    parser.add_argument("--output", default="data/finetune/train.jsonl")
    args = parser.parse_args()

    input_path = Path(args.input)
    output_path = Path(args.output)
    output_path.parent.mkdir(parents=True, exist_ok=True)

    total = 0
    kept = 0
    skipped_no_text = 0
    skipped_short = 0

    with open(input_path) as fin, open(output_path, "w") as fout:
        for line in fin:
            total += 1
            turn = json.loads(line)
            cleaned = extract_conversation(turn["messages"])

            if cleaned is None:
                skipped_no_text += 1
                continue

            kept += 1
            fout.write(json.dumps({"messages": cleaned}, ensure_ascii=False) + "\n")

    print(f"Total turns:    {total}")
    print(f"Kept:           {kept}")
    print(f"Skipped (no text pair): {skipped_no_text}")
    print(f"Output: {output_path}")

    # Show some samples
    print("\n--- Sample entries ---")
    with open(output_path) as f:
        for i, line in enumerate(f):
            if i >= 3:
                break
            turn = json.loads(line)
            msgs = turn["messages"]
            print(f"\n[Turn {i+1}] ({len(msgs)} messages)")
            for m in msgs:
                preview = m["content"][:80].replace("\n", " ")
                print(f"  {m['role']}: {preview}{'...' if len(m['content']) > 80 else ''}")


if __name__ == "__main__":
    main()
