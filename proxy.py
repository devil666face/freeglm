#!/usr/bin/env python3
import json
import os
import random
from socketserver import BaseServer
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen
from functools import partial

MODELS = {
    "glm-4.7": {
        "url": "https://api.z.ai/api/coding/paas/v4/chat/completions",
        "max_tokens": 8192,
    },
    "glm-4.7-flash": {
        "url": "https://api.z.ai/api/paas/v4/chat/completions",
        "max_tokens": 8192,
    },
}

MODEL_ALIASES = {
    "gpt-4": "glm-4.7",
    "gpt-4o": "glm-4.7",
    "gpt-4-turbo": "glm-4.7",
    "gpt-3.5-turbo": "glm-4.7-flash",
    "gpt-4o-mini": "glm-4.7-flash",
    "claude-3-sonnet": "glm-4.7",
    "claude-3-haiku": "glm-4.7-flash",
}

DEFAULT_MODEL = "glm-4.7-flash"
TIMEOUT = 120


def resolve_model(name):
    if name in MODELS:
        return name
    return MODEL_ALIASES.get(name, DEFAULT_MODEL)


def make_openai_id():
    chars = "abcdefghijklmnopqrstuvwxyz0123456789"
    return f"chatcmpl-{''.join(random.choice(chars) for _ in range(29))}"


def clamp_max_tokens(value, limit):
    try:
        if value is None:
            return min(4096, limit)
        return min(int(value), limit)
    except (ValueError, TypeError):
        return min(4096, limit)


def ensure_role(message, default="assistant"):
    if not message:
        return {"role": default, "content": ""}
    if "role" in message and message["role"]:
        return message
    msg_copy = dict(message)
    msg_copy.setdefault("role", default)
    return msg_copy


MESSAGE_LEVEL_FIELDS = {
    "tool_calls",
    "function_call",
    "reasoning_content",
    "metadata",
    "audio",
    "mcp_calls",
    "mcp_metadata",
}


class ProxyHandler(BaseHTTPRequestHandler):
    def __init__(
        self,
        request,
        client_address,
        server: BaseServer,
        key=None,
    ) -> None:
        self.key = key or ""
        super().__init__(request, client_address, server)

    def log_message(self, format, *args):
        ts = time.strftime("%H:%M:%S")
        msg = format % args if args else format
        print(f"[{ts}] {msg}")

    def handle(self):
        try:
            super().handle()
        except ConnectionResetError:
            self.log_message("⚠️ Connection reset by %s", self.client_address)

    def send_cors_headers(self):
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "*")

    def do_OPTIONS(self):
        self.send_response(200)
        self.send_cors_headers()
        self.end_headers()

    def do_GET(self):
        if self.path in ("/v1/models", "/models"):
            data = [
                {
                    "id": m,
                    "object": "model",
                    "created": 1700000000,
                    "owned_by": "zhipuai",
                }
                for m in MODELS
            ]
            self.send_json(200, {"object": "list", "data": data})
        elif self.path == "/health":
            self.send_json(200, {"status": "ok", "models": list(MODELS.keys())})
        else:
            self.send_error_json(404, "Not found")

    def do_POST(self):
        if self.path in ("/v1/chat/completions", "/chat/completions"):
            self.handle_chat()
        else:
            self.send_error_json(404, "Not found")

    def handle_chat(self):
        try:
            body = json.loads(
                self.rfile.read(int(self.headers.get("Content-Length", 0)))
            )
        except Exception as e:
            return self.send_error_json(400, f"Invalid body: {e}")

        requested = body.get("model", DEFAULT_MODEL)
        model = resolve_model(requested)
        cfg = MODELS[model]
        stream = body.get("stream", False)

        if requested != model:
            self.log_message(f"📎 {requested} → {model}")

        payload = dict(body)
        payload.setdefault("messages", [])
        if "temperature" not in payload:
            payload["temperature"] = 0.7
        payload["model"] = model
        payload["stream"] = stream
        payload["max_tokens"] = clamp_max_tokens(
            payload.get("max_tokens"), cfg["max_tokens"]
        )

        data = json.dumps(payload).encode("utf-8")
        req = Request(cfg["url"], data=data, method="POST")
        req.add_header("Authorization", f"Bearer {self.key}")
        req.add_header("Content-Type", "application/json")

        start = time.time()
        try:
            resp = urlopen(req, timeout=TIMEOUT)
        except HTTPError as e:
            err = e.read().decode("utf-8", errors="replace") if e.fp else ""
            self.log_message(f"❌ ZhipuAI {e.code} ({time.time() - start:.1f}с)")
            try:
                msg = json.loads(err).get("error", {}).get("message", err[:500])
            except Exception:
                msg = err[:500]
            return self.send_error_json(e.code, msg)
        except (URLError, TimeoutError) as e:
            return self.send_error_json(502, f"Connection error: {e}")

        if stream:
            self.do_stream(resp, model)
        else:
            self.do_normal(resp, model, time.time() - start)

    def do_normal(self, resp, model, elapsed):
        try:
            result = json.loads(resp.read().decode("utf-8"))
        except Exception as e:
            return self.send_error_json(502, f"Invalid response: {e}")

        openai_resp = self.normalize_response(result, model)
        usage = openai_resp.get("usage", {})
        self.log_message(
            f"✅ {model} → {usage.get('total_tokens', '?')} tok, {elapsed:.1f}с"
        )
        self.send_json(200, openai_resp)

    def do_stream(self, resp, model):
        self.close_connection = True
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.send_cors_headers()
        self.end_headers()

        chat_id = make_openai_id()
        total = ""
        done_sent = False
        try:
            for line in resp:
                line = line.decode("utf-8").strip()
                if not line or not line.startswith("data:"):
                    continue
                data_str = line[5:].strip()
                if data_str == "[DONE]":
                    self.wfile.write(b"data: [DONE]\n\n")
                    self.wfile.flush()
                    done_sent = True
                    break
                try:
                    chunk = json.loads(data_str)
                except json.JSONDecodeError:
                    continue
                choices = chunk.get("choices", [])
                normalized_choices = [
                    self.normalize_stream_choice(choice, idx)
                    for idx, choice in enumerate(choices)
                ]
                total += self.extract_text_from_choices(normalized_choices)
                oai = {
                    "id": chunk.get("id", chat_id),
                    "object": chunk.get("object", "chat.completion.chunk"),
                    "created": chunk.get("created", int(time.time())),
                    "model": chunk.get("model", model),
                    "choices": normalized_choices,
                }
                for extra in (
                    "usage",
                    "system_fingerprint",
                    "service_tier",
                    "metadata",
                ):
                    if extra in chunk:
                        oai[extra] = chunk[extra]
                self.wfile.write(
                    f"data: {json.dumps(oai, ensure_ascii=False)}\n\n".encode("utf-8")
                )
                self.wfile.flush()
        except Exception as e:
            self.log_message(f"⚠️ Stream error: {e}")
        finally:
            if not done_sent:
                self.wfile.write(b"data: [DONE]\n\n")
                self.wfile.flush()
        self.log_message(f"{model} → {len(total)} chars")

    def normalize_response(self, result, model):
        response = dict(result)
        response.setdefault("id", make_openai_id())
        response.setdefault("object", "chat.completion")
        response.setdefault("created", int(time.time()))
        response["model"] = model
        response["choices"] = [
            self.normalize_choice(choice, idx)
            for idx, choice in enumerate(result.get("choices", []))
        ]
        if not response["choices"]:
            response["choices"].append(
                {
                    "index": 0,
                    "message": {"role": "assistant", "content": ""},
                    "finish_reason": "stop",
                }
            )
        return response

    def normalize_choice(self, choice, idx):
        normalized = dict(choice)
        normalized["index"] = choice.get("index", idx)
        normalized["finish_reason"] = choice.get("finish_reason")
        message = choice.get("message")
        if not message and "delta" in choice:
            message = choice["delta"]
        normalized_message = ensure_role(message or {}, "assistant")
        if isinstance(normalized_message, dict):
            normalized_message = dict(normalized_message)
            for field in MESSAGE_LEVEL_FIELDS:
                if field in choice and field not in normalized_message:
                    normalized_message[field] = choice[field]
        normalized["message"] = normalized_message
        normalized.pop("delta", None)
        return {k: v for k, v in normalized.items() if v not in (None, {})}

    def normalize_stream_choice(self, choice, idx):
        normalized = dict(choice)
        normalized["index"] = choice.get("index", idx)
        normalized["finish_reason"] = choice.get("finish_reason")
        delta = choice.get("delta") or choice.get("message") or {}
        if isinstance(delta, dict):
            enriched_delta = dict(delta)
            for field in MESSAGE_LEVEL_FIELDS:
                if field in choice and field not in enriched_delta:
                    enriched_delta[field] = choice[field]
            delta = enriched_delta
        if delta:
            normalized["delta"] = delta
        normalized.pop("message", None)
        return {k: v for k, v in normalized.items() if v is not None}

    def extract_text_from_choices(self, choices):
        total = ""
        for choice in choices:
            delta = choice.get("delta", {})
            content = delta.get("content") if isinstance(delta, dict) else None
            if isinstance(content, str):
                total += content
            elif isinstance(content, list):
                for block in content:
                    if isinstance(block, dict):
                        total += block.get("text", "")
        return total

    def send_json(self, status, data):
        body = json.dumps(data, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Connection", "close")
        self.send_cors_headers()
        self.end_headers()
        self.wfile.write(body)
        self.wfile.flush()
        self.close_connection = True

    def send_error_json(self, status, message):
        self.send_json(
            status, {"error": {"message": message, "type": "api_error", "code": status}}
        )


class ThreadedHTTPServer(HTTPServer):
    def process_request(self, request, client_address):
        t = threading.Thread(
            target=self._handle, args=(request, client_address), daemon=True
        )
        t.start()

    def _handle(self, request, client_address):
        try:
            self.finish_request(request, client_address)
        except Exception:
            self.handle_error(request, client_address)
        finally:
            self.shutdown_request(request)

    def handle_error(self, request, client_address):
        exc_type, exc, _ = sys.exc_info()
        if isinstance(exc, ConnectionResetError):
            ts = time.strftime("%H:%M:%S")
            print(f"  [{ts}] ⚠️ Connection reset by {client_address}")
            return
        BaseServer.handle_error(self, request, client_address)


def main():
    port = 5000

    if "--port" in sys.argv or "-p" in sys.argv:
        flag = "--port" if "--port" in sys.argv else "-p"
        idx = sys.argv.index(flag) + 1
        if idx < len(sys.argv):
            port = int(sys.argv[idx])

    if (key := os.getenv("ZAI_API_KEY", "")) == "":
        print("set ZAI_API_KEY env")
        sys.exit(1)

    handler = partial(ProxyHandler, key=key)
    server = ThreadedHTTPServer(("0.0.0.0", port), handler)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        server.server_close()


if __name__ == "__main__":
    main()
