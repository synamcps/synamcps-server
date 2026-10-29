"""Canonical JSON must match Go securebus fixtures."""

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from synamcps_securebus.canonical import (
    b64url,
    canonical_json,
    hash_canonical,
    sign_ed25519,
    verify_ed25519,
)


def test_stable_sorted_keys_go_fixture():
    a = {"b": 1, "a": {"z": True, "y": "x"}}
    b = {"a": {"y": "x", "z": True}, "b": 1}
    ca = canonical_json(a)
    cb = canonical_json(b)
    assert ca == cb
    assert ca == b'{"a":{"y":"x","z":true},"b":1}'


def test_go_html_escape_ampersand_in_url():
    raw = canonical_json({"u": "https://x.com/?a=1&b=2"})
    assert raw == b'{"u":"https://x.com/?a=1\\u0026b=2"}'


def test_go_html_escape_angles():
    raw = canonical_json({"s": "a<b>c"})
    assert raw == b'{"s":"a\\u003cb\\u003ec"}'


def test_whole_floats_like_go():
    # Go json.Marshal(float64(1)) -> "1"
    assert canonical_json({"policyVersion": 1.0}) == b'{"policyVersion":1}'
    assert canonical_json({"messageId": 42}) == b'{"messageId":42}'


def test_sign_verify_roundtrip():
    priv = Ed25519PrivateKey.generate()
    env = {"objectId": "obj1", "nonce": "n1", "payload": {"x": 1}}
    sig = sign_ed25519(priv, env)
    assert verify_ed25519(priv.public_key(), env, sig)
    tampered = dict(env)
    tampered["nonce"] = "n2"
    assert not verify_ed25519(priv.public_key(), tampered, sig)
    assert len(b64url(sig)) > 20


def test_hash_stable():
    h1 = hash_canonical({"b": 1, "a": 2})
    h2 = hash_canonical({"a": 2, "b": 1})
    assert h1 == h2
    assert len(h1) == 32
