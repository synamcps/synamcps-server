#!/usr/bin/env python3
"""Attester worker: consume pending_review, score vs rule_text, signed attest."""

from __future__ import annotations

import argparse
import json
import sys
import time
from pathlib import Path
from typing import Any, Literal

from langchain_core.messages import HumanMessage, SystemMessage
from langchain_openai import ChatOpenAI
from pydantic import BaseModel, Field

ROOT = Path(__file__).resolve().parent
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from synamcps_securebus.client import AgentKey, SecureBusClient, load_config


class AttestDecision(BaseModel):
    status: Literal["ok", "deny"] = Field(description="ok to approve, deny to reject")
    comment: str = Field(description="Short reason matching the rule text")


def _llm(cfg: dict[str, Any]) -> ChatOpenAI:
    llm_cfg = cfg.get("llm") or {}
    kwargs: dict[str, Any] = {
        "model": llm_cfg.get("model") or "gpt-4o-mini",
        "api_key": llm_cfg.get("api_key") or "missing",
        "temperature": 0,
    }
    if llm_cfg.get("base_url"):
        kwargs["base_url"] = llm_cfg["base_url"]
    return ChatOpenAI(**kwargs)


def decide(cfg: dict[str, Any], payload: dict[str, Any]) -> AttestDecision:
    rule = (cfg.get("rule_text") or "").strip()
    llm = _llm(cfg).with_structured_output(AttestDecision)
    messages = [
        SystemMessage(
            content=(
                "You are a securebus attester. Decide ok or deny strictly by the rule text. "
                "Return only the structured decision."
            )
        ),
        HumanMessage(
            content=(
                f"## Rule text\n{rule}\n\n"
                f"## Message payload\n```json\n{json.dumps(payload, indent=2)}\n```"
            )
        ),
    ]
    return llm.invoke(messages)


def process_once(client: SecureBusClient, object_id: str, agent: AgentKey, cfg: dict[str, Any]) -> int:
    result = client.consume(object_id, consumer="attester", condition="pending_review", max_messages=5)
    messages = result.get("messages") or []
    for msg in messages:
        mid = int(msg["id"])
        payload = msg.get("payload") or {}
        print(f"reviewing message #{mid}: {json.dumps(payload)[:200]}")
        try:
            decision = decide(cfg, payload)
        except Exception as e:
            print(f"  LLM failed, denying: {e}")
            decision = AttestDecision(status="deny", comment=f"attester error: {e}")
        att = client.attest(object_id, agent, mid, decision.status, decision.comment)
        print(f"  attested {decision.status}: {decision.comment!r} (attestation id={att.get('id')})")
    return len(messages)


def main() -> int:
    parser = argparse.ArgumentParser(description="Securebus attester worker")
    parser.add_argument("--config", default="")
    parser.add_argument("--once", action="store_true", help="Single consume pass then exit")
    parser.add_argument("--interval", type=float, default=2.0)
    args = parser.parse_args()

    cfg = load_config(args.config or None)
    object_id = (cfg.get("object_id") or "").strip()
    if not cfg.get("token") or not object_id:
        print("Need token and object_id (run bootstrap.py)", file=sys.stderr)
        return 1

    key_dir = ROOT / cfg.get("key_dir", "keys")
    agent = AgentKey.load("attester", key_dir)
    client = SecureBusClient(cfg["base_url"], cfg["token"], cfg.get("token_id", ""))
    print(f"attester listening on object={object_id} condition=pending_review")
    try:
        while True:
            n = process_once(client, object_id, agent, cfg)
            if args.once:
                break
            if n == 0:
                time.sleep(args.interval)
    finally:
        client.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
