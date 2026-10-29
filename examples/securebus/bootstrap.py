#!/usr/bin/env python3
"""Register agent keys, create a securebus topic, and write a signed policy."""

from __future__ import annotations

import argparse
import sys
from datetime import datetime, timezone
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parent
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from synamcps_securebus.client import AgentKey, SecureBusClient, load_config


def build_policy(object_id: str, agents: dict[str, AgentKey]) -> dict:
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    return {
        "schema": "synamcps.securebus.policy/v1",
        "topicId": object_id,
        "version": 1,
        "issuedAt": now,
        "chain": False,
        "agents": {
            name: {
                "keyId": agent.registered_key_id,
                "alg": "ed25519",
                "publicKey": agent.public_key_b64,
            }
            for name, agent in agents.items()
        },
        "rules": {
            "publish": ["chat"],
            "attest": ["attester"],
            "subscribe": ["chat", "attester", "executor"],
        },
        "attestation": {
            "statuses": ["ok", "deny"],
            "commentMaxBytes": 4096,
            "supersede": True,
        },
        "conditions": {
            "pending_review": {
                "authors": ["chat"],
                "mode": "on_publish",
            },
            "approved": {
                "authors": ["chat"],
                "require": [{"agent": "attester", "status": "ok"}],
                "anyDeny": False,
                "mode": "on_condition",
            },
        },
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="Bootstrap securebus LangChain demo topic")
    parser.add_argument("--config", default="", help="Path to config.local.yaml")
    args = parser.parse_args()

    cfg = load_config(args.config or None)
    if not cfg.get("token"):
        print("Set token in config.local.yaml or SYNA_TOKEN", file=sys.stderr)
        return 1
    if not cfg.get("token_id"):
        print("Set token_id in config.local.yaml or SYNA_TOKEN_ID", file=sys.stderr)
        return 1

    key_dir = ROOT / cfg.get("key_dir", "keys")
    names = ("chat", "attester", "executor")
    agents: dict[str, AgentKey] = {}
    for name in names:
        pem = key_dir / f"{name}.pem"
        if pem.exists():
            agents[name] = AgentKey.load(name, key_dir)
        else:
            agents[name] = AgentKey.generate(name, key_dir)
            print(f"generated key for {name}")

    client = SecureBusClient(cfg["base_url"], cfg["token"], cfg["token_id"])
    try:
        for name, agent in agents.items():
            if agent.registered_key_id:
                print(f"{name}: already registered as {agent.registered_key_id}")
                continue
            client.register_key(agent)
            print(f"{name}: registered keyId={agent.registered_key_id}")

        object_id = (cfg.get("object_id") or "").strip()
        if not object_id:
            created = client.create_object(
                kind="securebus",
                slug=cfg.get("topic_slug", "securebus-demo"),
                name=cfg.get("topic_name", "Securebus LangChain Demo"),
            )
            object_id = created["id"]
            print(f"created securebus object id={object_id}")
        else:
            print(f"reusing object_id={object_id}")

        doc = build_policy(object_id, agents)
        signed = client.put_policy(object_id, doc)
        print(f"policy version={signed.get('version')} hash={signed.get('hash')}")

        out = dict(cfg)
        out["object_id"] = object_id
        out["agents"] = {
            name: {"key_id": agents[name].registered_key_id} for name in names
        }
        out_path = ROOT / "config.local.yaml"
        # Preserve secrets already present; never dump empty token over a filled file blindly.
        with out_path.open("w") as f:
            yaml.safe_dump(out, f, sort_keys=False, default_flow_style=False)
        print(f"wrote {out_path}")
    finally:
        client.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
