#!/usr/bin/env python3
"""Executor worker: consume approved messages, verify signatures, browse with Playwright."""

from __future__ import annotations

import argparse
import ipaddress
import json
import socket
import sys
import time
from pathlib import Path
from typing import Any
from urllib.parse import urlparse

ROOT = Path(__file__).resolve().parent
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from synamcps_securebus.client import AgentKey, SecureBusClient, load_config


def _is_public_https(url: str) -> tuple[bool, str]:
    try:
        parsed = urlparse(url)
    except Exception as e:
        return False, f"bad url: {e}"
    if parsed.scheme != "https":
        return False, "only https allowed"
    host = parsed.hostname or ""
    if not host:
        return False, "missing host"
    if host.lower() in ("localhost", "metadata.google.internal"):
        return False, "localhost blocked"
    try:
        infos = socket.getaddrinfo(host, 443, type=socket.SOCK_STREAM)
    except socket.gaierror as e:
        return False, f"dns failed: {e}"
    for info in infos:
        ip = ipaddress.ip_address(info[4][0])
        if (
            ip.is_private
            or ip.is_loopback
            or ip.is_link_local
            or ip.is_multicast
            or ip.is_reserved
            or ip.is_unspecified
        ):
            return False, f"non-public IP {ip}"
    return True, "ok"


def browse(url: str, max_chars: int = 4000) -> str:
    from playwright.sync_api import sync_playwright

    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        try:
            page = browser.new_page()
            page.goto(url, wait_until="domcontentloaded", timeout=30000)
            text = page.inner_text("body")
            title = page.title()
        finally:
            browser.close()
    text = " ".join(text.split())
    if len(text) > max_chars:
        text = text[:max_chars] + "…"
    return f"Title: {title}\n\n{text}"


def attester_ok(vr: dict[str, Any], attester_key_id: str = "") -> tuple[bool, str]:
    if not vr.get("signatureOk") or not vr.get("hashOk"):
        return False, "message signature/hash verify failed"
    checks_by_key = {
        c.get("signerKeyId"): c for c in (vr.get("attestationChecks") or []) if c.get("signerKeyId")
    }
    atts = vr.get("attestations") or []
    for a in atts:
        kid = a.get("signerKeyId") or ""
        if attester_key_id and kid != attester_key_id:
            continue
        check = checks_by_key.get(kid) or {}
        if check.get("signatureOk") is False or check.get("superseded"):
            continue
        status = a.get("status") or ""
        comment = a.get("comment") or ""
        if status == "deny":
            return False, f"deny from {kid}: {comment}"
        if status == "ok":
            return True, comment or "ok"
    return False, "no attester ok found"


def process_once(
    client: SecureBusClient,
    object_id: str,
    cfg: dict[str, Any],
    attester_key_id: str = "",
) -> int:
    result = client.consume(object_id, consumer="executor", condition="approved", max_messages=5)
    messages = result.get("messages") or []
    for msg in messages:
        mid = int(msg["id"])
        payload = msg.get("payload") or {}
        print(f"executor got message #{mid}")
        vr = client.verify(object_id, mid)
        ok, reason = attester_ok(vr, attester_key_id=attester_key_id)
        if not ok:
            print(f"  skip: {reason}")
            continue
        if payload.get("tool") != "browse":
            print(f"  skip: unsupported tool {payload.get('tool')!r}")
            continue
        args = payload.get("args") or {}
        url = (args.get("url") or "").strip()
        allowed, why = _is_public_https(url)
        if not allowed:
            print(f"  skip URL: {why}")
            continue
        print(f"  browsing {url} (attest: {reason})")
        try:
            body = browse(url)
        except Exception as e:
            print(f"  browse failed: {e}")
            continue
        print("--- browse result ---")
        print(body[:1500])
        print("---------------------")
        storage_id = (cfg.get("storage_id") or "").strip()
        if storage_id:
            try:
                client.create_knowledge(
                    storage_id=storage_id,
                    title=f"Browse result for message #{mid}",
                    content=f"URL: {url}\nAttest: {reason}\n\n{body}",
                    visibility="personal",
                )
                print("  saved knowledge note")
            except Exception as e:
                print(f"  knowledge save failed: {e}")
    return len(messages)


def main() -> int:
    parser = argparse.ArgumentParser(description="Securebus executor worker")
    parser.add_argument("--config", default="")
    parser.add_argument("--once", action="store_true")
    parser.add_argument("--interval", type=float, default=2.0)
    args = parser.parse_args()

    cfg = load_config(args.config or None)
    object_id = (cfg.get("object_id") or "").strip()
    if not cfg.get("token") or not object_id:
        print("Need token and object_id (run bootstrap.py)", file=sys.stderr)
        return 1

    # Executor needs a registered key only for policy membership (subscribe list);
    # consume itself is authorized by the access token + ACL.
    key_dir = ROOT / cfg.get("key_dir", "keys")
    _ = AgentKey.load("executor", key_dir)
    attester_key_id = ((cfg.get("agents") or {}).get("attester") or {}).get("key_id") or ""
    if not attester_key_id:
        try:
            attester_key_id = AgentKey.load("attester", key_dir).registered_key_id
        except FileNotFoundError:
            attester_key_id = ""

    client = SecureBusClient(cfg["base_url"], cfg["token"], cfg.get("token_id", ""))
    print(f"executor listening on object={object_id} condition=approved")
    try:
        while True:
            n = process_once(client, object_id, cfg, attester_key_id=attester_key_id)
            if args.once:
                break
            if n == 0:
                time.sleep(args.interval)
    finally:
        client.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
