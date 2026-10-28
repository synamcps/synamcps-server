# Securebus envelope specification

Private keys stay on the agent (or local sidecar / browser). SynaMCPs only stores public keys and verifies signatures.

## Canonicalization

1. Normalize the envelope to a JSON object.
2. Sort object keys recursively (RFC 8785-style).
3. Marshal without insignificant whitespace.
4. `hash = SHA-256(canonical_bytes)`.
5. Sign `hash`:
   - **ed25519**: `Ed25519.Sign(privateKey, hash)`
   - **ecdsa-p256**: ECDSA over `hash` (ASN.1 DER or IEEE P1363 `r||s`); WebCrypto uses `{name:"ECDSA", hash:"SHA-256"}` on the canonical UTF-8 bytes.

## Message envelope (`publish`)

```json
{
  "objectId": "<topic object id>",
  "policyVersion": 1,
  "policyHash": "<base64url of policy hash>",
  "authorKeyId": "<agent key id>",
  "nonce": "<unique per topic>",
  "createdAt": "<RFC3339Nano UTC>",
  "prevHash": "<optional previous message hash when policy.chain>",
  "payload": {}
}
```

## Attestation envelope (`attest`)

```json
{
  "messageId": 1,
  "messageHash": "<base64url>",
  "policyVersion": 1,
  "signerKeyId": "<agent key id>",
  "status": "ok",
  "comment": "",
  "createdAt": "<RFC3339Nano UTC>"
}
```

## Proof-of-possession (key registration)

Challenge: `POST /api/agent-keys/challenge` → `{nonce, expiresAt}`.

Sign:

```json
{
  "nonce": "<challenge nonce>",
  "publicKey": "<base64url raw public key>",
  "tokenId": "<access token id>"
}
```

Or for web sessions: `"sessionId": "session:<session_id cookie>"` instead of `tokenId`.

Register: `POST /api/agent-keys` with `{alg, publicKey, nonce, signature}`.

## Policy document

Schema id: `synamcps.securebus.policy/v1`. Server signs the canonical policy with the policy signing key. Agents verify using `GET /.well-known/synamcps-keys`.
