# Securebus LangChain example

Three agents share one SynaMCPs **securebus** topic:

| Agent | Role |
|-------|------|
| **Chat** (Streamlit) | Upload files to knowledge storage; LangChain decides when to request a browse; **publishes** a signed `tool_call` message (does not browse itself). |
| **Attester** | Consumes `pending_review`, scores the payload against `rule_text` with an LLM, **attests** `ok` or `deny`. |
| **Executor** | Consumes `approved` (attester `ok`), **verifies** signatures, then browses with Playwright. |

Private Ed25519 keys stay on disk under `keys/`. SynaMCPs only stores public keys and verifies signatures. See [docs/securebus-envelope.md](../../docs/securebus-envelope.md).

## Prerequisites

- Running SynaMCPs (`make compose-up`), Python 3.11+
- Admin: create a user access token (note both the **secret** and the **token id**) and a knowledge **storage**
- An OpenAI-compatible LLM API key (or local gateway)

## Setup

```bash
cd examples/securebus
python3 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
playwright install chromium

cp config.example.yaml config.local.yaml
# edit: token, token_id, storage_id, llm.api_key
# or export SYNA_TOKEN / SYNA_TOKEN_ID / STORAGE_ID / OPENAI_API_KEY

python bootstrap.py
```

`bootstrap.py` generates three keys, registers them via PoP, creates the securebus object, and writes a policy with:

- `pending_review` — new messages from `chat` (attester)
- `approved` — same messages after attester `ok` (executor)

## Run (three terminals)

```bash
# 1) Chat UI
streamlit run chat_app.py

# 2) Attester
python attester.py

# 3) Executor
python executor.py
```

In the chat: upload a file (optional), then ask to browse a public `https` URL. Watch the attester approve/deny, then the executor fetch the page.

## Tests

```bash
pip install pytest
PYTHONPATH=. pytest -q
```

Canonical JSON tests lock compatibility with the Go securebus signer (including `&` → `\u0026`).

## Config notes

- One access token is enough; agents are distinguished by **keys in the topic policy**, not separate tokens.
- `rule_text` in config drives the attester LLM. Default rules allow only public `https` browse calls.
- `config.local.yaml` and `keys/` are gitignored.
