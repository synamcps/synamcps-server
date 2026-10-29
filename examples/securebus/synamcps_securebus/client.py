"""HTTP client for SynaMCPs securebus + knowledge ingest."""

from __future__ import annotations

import os
import time
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import httpx
import yaml
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives.serialization import (
    Encoding,
    NoEncryption,
    PrivateFormat,
    PublicFormat,
    load_pem_private_key,
)

from .canonical import b64url, sign_ed25519


def load_config(path: str | Path | None = None) -> dict[str, Any]:
    """Load YAML config; env vars override sensitive fields."""
    root = Path(__file__).resolve().parents[1]
    cfg_path = Path(path) if path else root / "config.local.yaml"
    if not cfg_path.exists():
        cfg_path = root / "config.example.yaml"
    with cfg_path.open() as f:
        cfg = yaml.safe_load(f) or {}

    cfg["base_url"] = os.environ.get("SYNA_BASE_URL", cfg.get("base_url", "http://localhost:8080")).rstrip("/")
    if os.environ.get("SYNA_TOKEN"):
        cfg["token"] = os.environ["SYNA_TOKEN"]
    if os.environ.get("SYNA_TOKEN_ID"):
        cfg["token_id"] = os.environ["SYNA_TOKEN_ID"]
    if os.environ.get("STORAGE_ID"):
        cfg["storage_id"] = os.environ["STORAGE_ID"]
    if os.environ.get("OPENAI_API_KEY"):
        cfg.setdefault("llm", {})["api_key"] = os.environ["OPENAI_API_KEY"]
    if os.environ.get("OPENAI_BASE_URL"):
        cfg.setdefault("llm", {})["base_url"] = os.environ["OPENAI_BASE_URL"]
    return cfg


def _rfc3339_nano() -> str:
    now = datetime.now(timezone.utc)
    # Match Go time.RFC3339Nano (variable fractional digits, truncate trailing zeros).
    return now.isoformat().replace("+00:00", "Z")


class AgentKey:
    """Ed25519 key pair stored on disk for one named agent."""

    def __init__(self, name: str, key_dir: Path, private_key: Ed25519PrivateKey, registered_key_id: str = ""):
        self.name = name
        self.key_dir = key_dir
        self.private_key = private_key
        self.registered_key_id = registered_key_id

    @property
    def public_key_raw(self) -> bytes:
        return self.private_key.public_key().public_bytes(Encoding.Raw, PublicFormat.Raw)

    @property
    def public_key_b64(self) -> str:
        return b64url(self.public_key_raw)

    @classmethod
    def generate(cls, name: str, key_dir: Path) -> "AgentKey":
        key_dir.mkdir(parents=True, exist_ok=True)
        priv = Ed25519PrivateKey.generate()
        agent = cls(name, key_dir, priv)
        agent.save()
        return agent

    @classmethod
    def load(cls, name: str, key_dir: Path) -> "AgentKey":
        pem_path = key_dir / f"{name}.pem"
        if not pem_path.exists():
            raise FileNotFoundError(f"missing key for {name}: {pem_path}")
        priv = load_pem_private_key(pem_path.read_bytes(), password=None)
        if not isinstance(priv, Ed25519PrivateKey):
            raise TypeError(f"expected Ed25519 key for {name}")
        kid = ""
        kid_path = key_dir / f"{name}.key_id"
        if kid_path.exists():
            kid = kid_path.read_text().strip()
        return cls(name, key_dir, priv, kid)

    def save(self) -> None:
        self.key_dir.mkdir(parents=True, exist_ok=True)
        pem = self.private_key.private_bytes(Encoding.PEM, PrivateFormat.PKCS8, NoEncryption())
        path = self.key_dir / f"{self.name}.pem"
        path.write_bytes(pem)
        path.chmod(0o600)
        pub_path = self.key_dir / f"{self.name}.pub"
        pub_path.write_text(self.public_key_b64 + "\n")
        if self.registered_key_id:
            (self.key_dir / f"{self.name}.key_id").write_text(self.registered_key_id + "\n")


class SecureBusClient:
    def __init__(self, base_url: str, token: str, token_id: str = "", timeout: float = 60.0):
        self.base_url = base_url.rstrip("/")
        self.token = token
        self.token_id = token_id
        self._http = httpx.Client(
            base_url=self.base_url,
            headers={"Authorization": f"Bearer {token}"},
            timeout=timeout,
        )

    def close(self) -> None:
        self._http.close()

    def __enter__(self) -> "SecureBusClient":
        return self

    def __exit__(self, *args: Any) -> None:
        self.close()

    def _request(self, method: str, path: str, json_body: Any | None = None, **kwargs: Any) -> Any:
        r = self._http.request(method, path, json=json_body, **kwargs)
        if r.status_code >= 400:
            raise RuntimeError(f"{method} {path} -> {r.status_code}: {r.text}")
        if not r.content:
            return None
        ct = r.headers.get("content-type", "")
        if "application/json" in ct:
            return r.json()
        return r.text

    # --- agent keys ---

    def challenge(self) -> dict[str, Any]:
        return self._request("POST", "/api/agent-keys/challenge", {})

    def register_key(self, agent: AgentKey) -> dict[str, Any]:
        if not self.token_id:
            raise RuntimeError("token_id required for PoP registration (SYNA_TOKEN_ID)")
        ch = self.challenge()
        nonce = ch["nonce"]
        env = {
            "nonce": nonce,
            "publicKey": agent.public_key_b64,
            "tokenId": self.token_id,
        }
        sig = sign_ed25519(agent.private_key, env)
        reg = self._request(
            "POST",
            "/api/agent-keys",
            {
                "alg": "ed25519",
                "publicKey": agent.public_key_b64,
                "nonce": nonce,
                "signature": b64url(sig),
            },
        )
        kid = reg.get("keyId") or reg.get("id") or ""
        if not kid:
            raise RuntimeError(f"register response missing keyId: {reg}")
        agent.registered_key_id = kid
        agent.save()
        return reg

    def list_keys(self) -> Any:
        return self._request("GET", "/api/agent-keys")

    # --- objects / policy ---

    def create_object(self, kind: str, slug: str, name: str, backend: str = "", config: dict | None = None) -> dict[str, Any]:
        return self._request(
            "POST",
            "/api/objects",
            {"kind": kind, "slug": slug, "name": name, "backend": backend, "config": config or {}},
        )

    def put_policy(self, object_id: str, document: dict[str, Any]) -> dict[str, Any]:
        return self._request("PUT", f"/api/objects/{object_id}/policy", document)

    def get_policy(self, object_id: str, version: int | None = None) -> dict[str, Any]:
        params = {}
        if version is not None:
            params["version"] = str(version)
        r = self._http.get(f"/api/securebus/{object_id}/policy", params=params)
        if r.status_code >= 400:
            raise RuntimeError(f"GET policy -> {r.status_code}: {r.text}")
        return r.json()

    # --- securebus methods ---

    def publish(self, object_id: str, agent: AgentKey, payload: dict[str, Any], nonce: str = "") -> dict[str, Any]:
        if not agent.registered_key_id:
            raise RuntimeError(f"agent {agent.name} is not registered")
        pol = self.get_policy(object_id)
        version = int(pol.get("version") or pol.get("document", {}).get("version") or 1)
        policy_hash = pol.get("hash") or ""
        if not nonce:
            nonce = f"n-{uuid.uuid4().hex}"
        created_at = _rfc3339_nano()
        env = {
            "objectId": object_id,
            "policyVersion": version,
            "policyHash": policy_hash,
            "authorKeyId": agent.registered_key_id,
            "nonce": nonce,
            "createdAt": created_at,
            "payload": payload,
        }
        sig = sign_ed25519(agent.private_key, env)
        return self._request(
            "POST",
            f"/api/securebus/{object_id}/publish",
            {
                "keyId": agent.registered_key_id,
                "policyVersion": version,
                "policyHash": policy_hash,
                "nonce": nonce,
                "createdAt": created_at,
                "payload": payload,
                "signature": b64url(sig),
            },
        )

    def attest(
        self,
        object_id: str,
        agent: AgentKey,
        message_id: int,
        status: str,
        comment: str = "",
    ) -> dict[str, Any]:
        if not agent.registered_key_id:
            raise RuntimeError(f"agent {agent.name} is not registered")
        pol = self.get_policy(object_id)
        version = int(pol.get("version") or pol.get("document", {}).get("version") or 1)
        vr = self.verify(object_id, message_id)
        msg = vr.get("message") or {}
        message_hash = msg.get("hash") or ""
        created_at = _rfc3339_nano()
        env = {
            "messageId": message_id,
            "messageHash": message_hash,
            "policyVersion": version,
            "signerKeyId": agent.registered_key_id,
            "status": status,
            "comment": comment,
            "createdAt": created_at,
        }
        sig = sign_ed25519(agent.private_key, env)
        return self._request(
            "POST",
            f"/api/securebus/{object_id}/attest",
            {
                "keyId": agent.registered_key_id,
                "messageId": message_id,
                "policyVersion": version,
                "status": status,
                "comment": comment,
                "createdAt": created_at,
                "signature": b64url(sig),
            },
        )

    def verify(self, object_id: str, message_id: int) -> dict[str, Any]:
        return self._request("POST", f"/api/securebus/{object_id}/verify", {"messageId": message_id})

    def consume(
        self,
        object_id: str,
        consumer: str,
        condition: str,
        max_messages: int = 10,
    ) -> dict[str, Any]:
        return self._request(
            "POST",
            f"/api/securebus/{object_id}/consume",
            {"consumer": consumer, "condition": condition, "max": max_messages},
        )

    def ingest_file(
        self,
        storage_id: str,
        filename: str,
        content: bytes,
        title: str = "",
        visibility: str = "personal",
        mime_type: str = "",
    ) -> dict[str, Any]:
        files = {"file": (filename, content, mime_type or "application/octet-stream")}
        data = {
            "storageId": storage_id,
            "title": title or filename,
            "visibility": visibility,
            "source": "securebus-example",
        }
        r = self._http.post("/api/knowledge/ingest/file", data=data, files=files)
        if r.status_code >= 400:
            raise RuntimeError(f"ingest file -> {r.status_code}: {r.text}")
        return r.json()

    def create_knowledge(
        self,
        storage_id: str,
        title: str,
        content: str,
        visibility: str = "personal",
    ) -> dict[str, Any]:
        return self._request(
            "POST",
            "/api/knowledge",
            {
                "storageId": storage_id,
                "title": title,
                "text": content,
                "mimeType": "text/plain",
                "visibility": visibility,
                "source": "securebus-executor",
            },
        )


def poll_until(
    fn,
    *,
    timeout_s: float = 60.0,
    interval_s: float = 1.0,
    pred=None,
):
    """Poll fn() until pred(result) is true or timeout."""
    deadline = time.time() + timeout_s
    last = None
    while time.time() < deadline:
        last = fn()
        if pred is None or pred(last):
            return last
        time.sleep(interval_s)
    return last
