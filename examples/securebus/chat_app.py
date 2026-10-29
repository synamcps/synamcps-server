#!/usr/bin/env python3
"""Streamlit chat agent: upload files, publish browse tool_calls to securebus."""

from __future__ import annotations

import json
import sys
import time
from pathlib import Path
from typing import Any

import streamlit as st
from langchain_core.messages import AIMessage, HumanMessage, SystemMessage, ToolMessage
from langchain_core.tools import tool
from langchain_openai import ChatOpenAI

ROOT = Path(__file__).resolve().parent
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from synamcps_securebus.client import AgentKey, SecureBusClient, load_config


SYSTEM_PROMPT = """You are a helpful chat agent. You cannot browse the web yourself.
When the user asks you to open or fetch a URL, call request_browse with that URL.
If they uploaded files, mention them in your reply and pass context in user_message.
Do not invent browse results; the Executor agent will run browsing after attestation."""


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


def _attestation_status(vr: dict[str, Any]) -> tuple[str, str]:
    """Return (status_label, detail) from a verify response."""
    if not vr.get("signatureOk") or not vr.get("hashOk"):
        return "error", "message signature/hash invalid"
    checks = {c.get("signerKeyId"): c for c in (vr.get("attestationChecks") or [])}
    atts = vr.get("attestations") or []
    for a in atts:
        kid = a.get("signerKeyId")
        check = checks.get(kid) or {}
        if check.get("superseded"):
            continue
        if check.get("signatureOk") is False:
            continue
        if a.get("status") == "deny":
            return "deny", a.get("comment") or "denied"
        if a.get("status") == "ok":
            return "ok", a.get("comment") or "approved"
    if not atts:
        return "pending", "awaiting attester"
    return "pending", "awaiting valid attestation"


def main() -> None:
    st.set_page_config(page_title="Securebus Chat", page_icon=None, layout="centered")
    st.title("Securebus Chat")
    st.caption("Publishes signed tool_call messages; Attester and Executor run separately.")

    cfg = load_config()
    object_id = (cfg.get("object_id") or "").strip()
    storage_id = (cfg.get("storage_id") or "").strip()
    if not cfg.get("token") or not object_id:
        st.error("Run bootstrap.py and set token / object_id in config.local.yaml")
        st.stop()

    key_dir = ROOT / cfg.get("key_dir", "keys")
    try:
        chat_key = AgentKey.load("chat", key_dir)
    except FileNotFoundError:
        st.error("Missing chat key; run bootstrap.py first")
        st.stop()

    if "messages" not in st.session_state:
        st.session_state.messages = []
    if "files" not in st.session_state:
        st.session_state.files = []
    if "pending_msg" not in st.session_state:
        st.session_state.pending_msg = None

    uploaded = st.file_uploader("Upload files into knowledge storage", accept_multiple_files=True)
    if uploaded and storage_id:
        client = SecureBusClient(cfg["base_url"], cfg["token"], cfg.get("token_id", ""))
        try:
            for uf in uploaded:
                # Avoid re-ingesting the same Streamlit upload object repeatedly.
                marker = f"{uf.name}:{uf.size}"
                if any(f.get("marker") == marker for f in st.session_state.files):
                    continue
                doc = client.ingest_file(
                    storage_id=storage_id,
                    filename=uf.name,
                    content=uf.getvalue(),
                    title=uf.name,
                    visibility="personal",
                    mime_type=uf.type or "",
                )
                st.session_state.files.append(
                    {
                        "marker": marker,
                        "doc_id": doc.get("id") or doc.get("docId") or "",
                        "filename": uf.name,
                    }
                )
                st.success(f"Ingested {uf.name}")
        except Exception as e:
            st.error(f"Ingest failed: {e}")
        finally:
            client.close()
    elif uploaded and not storage_id:
        st.warning("Set storage_id to enable uploads")

    if st.session_state.files:
        st.write("Attached:", ", ".join(f["filename"] for f in st.session_state.files))

    for m in st.session_state.messages:
        with st.chat_message(m["role"]):
            st.markdown(m["content"])

    # Poll pending securebus message for attestation status.
    if st.session_state.pending_msg:
        mid = st.session_state.pending_msg
        client = SecureBusClient(cfg["base_url"], cfg["token"], cfg.get("token_id", ""))
        try:
            vr = client.verify(object_id, mid)
            status, detail = _attestation_status(vr)
            st.info(f"Message #{mid}: **{status}** — {detail}")
            if status in ("ok", "deny"):
                st.session_state.pending_msg = None
            else:
                time.sleep(0.2)
                st.rerun()
        except Exception as e:
            st.warning(f"verify poll: {e}")
        finally:
            client.close()

    prompt = st.chat_input("Ask to browse a URL (optionally after uploading files)")
    if not prompt:
        return

    st.session_state.messages.append({"role": "user", "content": prompt})
    with st.chat_message("user"):
        st.markdown(prompt)

    published: dict[str, Any] = {"message_id": None, "error": None}

    @tool
    def request_browse(url: str, user_message: str = "") -> str:
        """Request that the Executor browse a public https URL after attestation.

        Args:
            url: Absolute https URL to fetch.
            user_message: Short context from the user (include why the URL matters).
        """
        client = SecureBusClient(cfg["base_url"], cfg["token"], cfg.get("token_id", ""))
        try:
            payload = {
                "kind": "tool_call",
                "tool": "browse",
                "args": {"url": url},
                "user_message": user_message or prompt,
                "files": [
                    {"doc_id": f["doc_id"], "filename": f["filename"]}
                    for f in st.session_state.files
                    if f.get("doc_id")
                ],
            }
            msg = client.publish(object_id, chat_key, payload)
            mid = int(msg.get("id") or 0)
            published["message_id"] = mid
            return f"Published browse request as securebus message #{mid}. Waiting for attester."
        except Exception as e:
            published["error"] = str(e)
            return f"Failed to publish: {e}"
        finally:
            client.close()

    llm = _llm(cfg).bind_tools([request_browse])
    history = [SystemMessage(content=SYSTEM_PROMPT)]
    for m in st.session_state.messages:
        if m["role"] == "user":
            history.append(HumanMessage(content=m["content"]))
        else:
            history.append(AIMessage(content=m["content"]))

    with st.chat_message("assistant"):
        try:
            ai = llm.invoke(history)
            # Handle tool calls.
            if getattr(ai, "tool_calls", None):
                tool_msgs = []
                for tc in ai.tool_calls:
                    if tc["name"] == "request_browse":
                        result = request_browse.invoke(tc["args"])
                        tool_msgs.append(ToolMessage(content=result, tool_call_id=tc["id"]))
                follow = llm.invoke(history + [ai] + tool_msgs)
                text = follow.content if isinstance(follow.content, str) else str(follow.content)
            else:
                text = ai.content if isinstance(ai.content, str) else str(ai.content)
        except Exception as e:
            text = f"LLM error: {e}"

        st.markdown(text)
        st.session_state.messages.append({"role": "assistant", "content": text})
        if published["message_id"]:
            st.session_state.pending_msg = published["message_id"]
            st.rerun()


if __name__ == "__main__":
    main()
