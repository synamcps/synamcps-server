"""RFC 8785-style canonical JSON matching SynaMCPs Go securebus.

Go's encoding/json.Marshal HTML-escapes &, <, >, U+2028, and U+2029, and
marshals whole float64 values without a trailing ".0". Signatures must match.
"""

from __future__ import annotations

import base64
import hashlib
from typing import Any

from cryptography.hazmat.primitives.asymmetric.ed25519 import (
    Ed25519PrivateKey,
    Ed25519PublicKey,
)


def _normalize(v: Any) -> Any:
    if v is None or isinstance(v, bool) or isinstance(v, str):
        return v
    if isinstance(v, int) and not isinstance(v, bool):
        return v
    if isinstance(v, float):
        if v.is_integer():
            return int(v)
        return v
    if isinstance(v, dict):
        return {k: _normalize(v[k]) for k in sorted(v.keys())}
    if isinstance(v, (list, tuple)):
        return [_normalize(item) for item in v]
    # Fall back through JSON round-trip for unexpected types (datetime, etc.).
    import json

    return _normalize(json.loads(json.dumps(v, default=str)))


def _escape_go_string(s: str) -> str:
    """Escape a string the way Go encoding/json does (with HTMLEscape)."""
    out: list[str] = []
    for ch in s:
        o = ord(ch)
        if ch == '"':
            out.append('\\"')
        elif ch == "\\":
            out.append("\\\\")
        elif ch == "\b":
            out.append("\\b")
        elif ch == "\f":
            out.append("\\f")
        elif ch == "\n":
            out.append("\\n")
        elif ch == "\r":
            out.append("\\r")
        elif ch == "\t":
            out.append("\\t")
        elif ch == "&":
            out.append("\\u0026")
        elif ch == "<":
            out.append("\\u003c")
        elif ch == ">":
            out.append("\\u003e")
        elif o == 0x2028:
            out.append("\\u2028")
        elif o == 0x2029:
            out.append("\\u2029")
        elif o < 0x20:
            out.append(f"\\u{o:04x}")
        else:
            out.append(ch)
    return "".join(out)


def _encode(v: Any) -> str:
    if v is None:
        return "null"
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, str):
        return '"' + _escape_go_string(v) + '"'
    if isinstance(v, int) and not isinstance(v, bool):
        return str(v)
    if isinstance(v, float):
        # Match Go: fmt via encoding/json (ES6-ish); keep simple for demo payloads.
        if v != v:  # NaN
            raise ValueError("NaN not allowed in canonical JSON")
        if v == float("inf") or v == float("-inf"):
            raise ValueError("Infinity not allowed in canonical JSON")
        text = repr(v)
        if text.endswith(".0") and "." in text and "e" not in text and "E" not in text:
            text = text[:-2]
        return text
    if isinstance(v, dict):
        parts = []
        for k in sorted(v.keys()):
            parts.append(_encode(k) + ":" + _encode(v[k]))
        return "{" + ",".join(parts) + "}"
    if isinstance(v, list):
        return "[" + ",".join(_encode(item) for item in v) + "]"
    raise TypeError(f"unsupported type {type(v)!r}")


def canonical_json(v: Any) -> bytes:
    """Return deterministic JSON bytes compatible with Go CanonicalJSON."""
    return _encode(_normalize(v)).encode("utf-8")


def hash_canonical(v: Any) -> bytes:
    return hashlib.sha256(canonical_json(v)).digest()


def b64url(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode("ascii")


def decode_b64url(s: str) -> bytes:
    if not s:
        raise ValueError("empty encoding")
    pad = "=" * (-len(s) % 4)
    try:
        return base64.urlsafe_b64decode(s + pad)
    except Exception:
        return base64.b64decode(s)


def sign_ed25519(private_key: Ed25519PrivateKey, envelope: Any) -> bytes:
    return private_key.sign(hash_canonical(envelope))


def verify_ed25519(public_key: Ed25519PublicKey, envelope: Any, signature: bytes) -> bool:
    try:
        public_key.verify(signature, hash_canonical(envelope))
        return True
    except Exception:
        return False
