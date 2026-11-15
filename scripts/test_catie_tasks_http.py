"""Synthetic process-level HTTP acceptance; never contacts a real model service."""
import argparse
import collections
import http.client
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlsplit


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def sse(choices, usage=None):
    event = {"id": "synthetic", "object": "chat.completion.chunk",
             "model": "synthetic-model", "choices": choices}
    if usage is not None:
        event["usage"] = usage
    return "data: " + json.dumps(event, ensure_ascii=False, separators=(",", ":")) + "\n\n"


def choice(index=0, text=None, finish=None, **fields):
    delta = dict(fields)
    if text is not None:
        delta["content"] = text
    return {"index": index, "delta": delta, "finish_reason": finish}


def parse_sse(wire):
    events, done = [], 0
    for frame in wire.replace("\r\n", "\n").split("\n\n"):
        payload = "\n".join(line[5:].lstrip(" ") for line in frame.splitlines() if line.startswith("data:"))
        if payload == "[DONE]":
            done += 1
        elif payload:
            events.append(json.loads(payload))
    return events, done


def main():
    parser = argparse.ArgumentParser()
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--binary")
    source.add_argument("--image")
    parser.add_argument("--expected-commit", required=True)
    parser.add_argument("--expected-version", required=True)
    parser.add_argument("--report", required=True)
    args = parser.parse_args()
    if args.binary:
        args.binary = str(Path(args.binary).resolve())
    checks = []
    upstream_key = "synthetic-" + secrets.token_hex(12)
    errors = []
    lock = threading.Lock()
    counts = collections.Counter()
    active = {}
    polled = collections.defaultdict(threading.Event)
    cancelled = collections.defaultdict(threading.Event)
    ordinary_text = "LONG_E2E_BEGIN\n" + "这是用于验证传输的安全合成正文，记录平静的天气和日常工作。" * 27 + "\nLONG_E2E_END"
    usage = {"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}

    class Mock(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *_):
            pass

        def send(self, body, status=200, headers=None):
            raw = body.encode() if isinstance(body, str) else json.dumps(body, ensure_ascii=False).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.send_header("Connection", "close")
            for key, value in (headers or {}).items():
                self.send_header(key, value)
            self.end_headers()
            try:
                self.wfile.write(raw)
            except (BrokenPipeError, ConnectionResetError):
                pass
            self.close_connection = True

        def do_POST(self):
            target = urlsplit(self.path)
            if target.hostname != "192.0.2.1" or self.headers.get("Authorization") != "Bearer " + upstream_key:
                errors.append("mock received unexpected target/authentication")
                self.send({}, 400)
                return
            if target.path.endswith("/cancel"):
                task_id = target.path.split("/")[-2]
                case = active[task_id]
                with lock:
                    counts[(case, "cancel")] += 1
                cancelled[case].set()
                self.send({"status": "cancelled"})
                return
            body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
            case = body["model"].split("/", 1)[1]
            if body["model"] != "provider-full/" + case or body.get("max_tokens") != 32768:
                errors.append("final model or output budget changed")
            if self.headers.get("X-Acceptance") != "preserved":
                errors.append("effective endpoint header was lost")
            with lock:
                counts[(case, "post")] += 1
            if target.path != "/v1/openai/tasks":
                errors.append("task request used direct completion endpoint")
                self.send({}, 400)
            elif case == "submission_unknown":
                self.send({"status": "queued"})
            else:
                task_id = "synthetic-task-" + case
                active[task_id] = case
                self.send({"id": task_id}, headers={"X-Request-ID": "synthetic-upstream", "CF-Ray": "synthetic-ray", "Rndr-Id": "synthetic-render"})

        def do_GET(self):
            target = urlsplit(self.path)
            case = active[target.path.split("/")[-1]]
            cursor = int(parse_qs(target.query)["after"][0])
            with lock:
                counts[(case, "get")] += 1
                ordinal = counts[(case, "get")]
            polled[case].set()
            if case == "query_retry" and ordinal == 1:
                self.send({"error": {"code": "temporarily_unavailable"}}, 503, {"Retry-After": "1"})
                return
            if case == "cancel":
                self.send({"status": "running", "done": False, "next_after": cursor, "chunks": []})
                return
            if case == "empty_batch" and ordinal == 1:
                self.send({"status": "running", "done": False, "next_after": 0, "chunks": []})
                return
            if case == "nonstream":
                self.send({"status": "completed", "done": True, "result": {
                    "id": "synthetic", "object": "chat.completion", "model": "synthetic-model",
                    "choices": [{"index": 0, "message": {"role": "assistant", "content": "safe complete"}, "finish_reason": "stop"}], "usage": usage}})
                return
            finish = "content_filter" if case.startswith("filter") else "max_tokens" if case == "length" else "stop"
            text = "" if case == "filter_empty" else ordinary_text if case == "normal" else "safe partial"
            wire = sse([choice(text=text, finish=finish)], usage) + "data: [DONE]\n\n"
            if case == "missing_finish":
                wire = sse([choice(text="safe partial")])
            elif case == "mixed_choices":
                wire = sse([choice(0, "safe first", "content_filter")]) + sse([choice(1, "safe second", "stop")], usage) + "data: [DONE]\n\n"
            elif case == "split":
                full = ": upstream-control\n\n" + sse([choice(role="assistant")]) + sse([choice(reasoning_content="safe thought")]) + wire
                split = full.index("safe partial") + 4
                if cursor == 0:
                    self.send({"status": "running", "done": False, "next_after": 1, "chunks": [full[:split]]})
                    return
                wire = full[split:]
            elif case == "duplicate_terminal":
                wire = sse([choice(text="safe partial", finish="stop")]) + sse([choice(finish="stop")], usage) + "data: [DONE]\n\n"
            self.send({"status": "completed", "done": True, "next_after": cursor + 1, "chunks": [wire]})

    proxy = ThreadingHTTPServer(("127.0.0.1", 0), Mock)
    proxy.daemon_threads = True
    thread = threading.Thread(target=proxy.serve_forever, daemon=True)
    thread.start()
    port = free_port()
    base = "http://127.0.0.1:" + str(port)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    token = None

    def api(path, body=None, auth=None):
        headers = {"Content-Type": "application/json"}
        if auth or token:
            headers["Authorization"] = "Bearer " + (auth or token)
        request = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers=headers)
        with opener.open(request, timeout=12) as response:
            return json.load(response)["data"]

    with tempfile.TemporaryDirectory(prefix="octopus-http-acceptance-") as temp:
        directory = Path(temp)
        admin_password = secrets.token_urlsafe(24)
        values = {"OCTOPUS_SERVER_HOST": "127.0.0.1", "OCTOPUS_SERVER_PORT": str(port),
                  "OCTOPUS_DATABASE_TYPE": "sqlite", "OCTOPUS_AUTH_JWT_SECRET": secrets.token_hex(32),
                  "OCTOPUS_INITIAL_ADMIN_USERNAME": "acceptance", "OCTOPUS_INITIAL_ADMIN_PASSWORD": admin_password,
                  "OCTOPUS_EXTERNAL_LLM_PRICE_URL": "http://127.0.0.1:1/disabled"}
        container_name = "octopus-http-" + secrets.token_hex(6)
        if args.image:
            values.update(OCTOPUS_DATA_DIR="/acceptance/data", OCTOPUS_DATABASE_PATH="/acceptance/data/database.sqlite")
            command = ["docker", "run", "--rm", "--name", container_name, "--network", "host",
                       "--user", str(os.getuid()) + ":" + str(os.getgid()), "--entrypoint", "/app/octopus",
                       "-v", str(directory) + ":/acceptance"]
            for key, value in values.items():
                command.extend(["-e", key + "=" + value])
            command.extend([args.image, "start"])
            environment = os.environ.copy()
        else:
            values.update(OCTOPUS_DATA_DIR=str(directory / "data"), OCTOPUS_DATABASE_PATH=str(directory / "database.sqlite"))
            command = [args.binary, "start"]
            environment = {**os.environ, **values}
        log_path = directory / "app.log"
        with log_path.open("w") as log:
            process = subprocess.Popen(command, cwd=directory, env=environment, stdout=log, stderr=subprocess.STDOUT)
        try:
            deadline = time.monotonic() + 50
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    raise AssertionError("candidate process exited during startup: " + log_path.read_text(errors="replace")[-4000:])
                try:
                    api("/api/v1/bootstrap/status")
                    break
                except (OSError, ValueError):
                    time.sleep(0.2)
            else:
                raise AssertionError("candidate did not start")
            token = api("/api/v1/user/login", {"username": "acceptance", "password": admin_password})["token"]
            assert api("/api/v1/update/now-version") == args.expected_version
            # The startup banner intentionally abbreviates the commit. The image
            # workflow separately validates the full immutable OCI revision.
            assert args.expected_commit[:8] in log_path.read_text(errors="replace"), "startup commit does not match candidate"
            checks.append("source_identity")
            for key, value in {"relay_log_keep_enabled": "true", "relay_log_content_enabled": "false",
                               "retry_empty_output": "true", "relay_retry_count": "3", "relay_route_retries": "3"}.items():
                api("/api/v1/setting/set", {"key": key, "value": value})
            cases = ["normal", "filter_body", "filter_empty", "length", "mixed_choices", "split",
                     "empty_batch", "query_retry", "duplicate_terminal", "missing_finish", "submission_unknown", "nonstream", "cancel"]
            channel_payload = {"name": "synthetic-tasks", "type": 0, "enabled": True,
                               "model": ",".join("provider-full/" + case for case in cases),
                               "proxy_mode": "pool", "channel_proxy": "http://127.0.0.1:" + str(proxy.server_port),
                               "keys": [{"enabled": True, "channel_key": upstream_key}],
                               "connection_config": {"version": 2, "selection": "configured", "catalog": {"format": "manual"},
                                 "endpoints": [{"id": "synthetic-tasks", "protocol": "chat", "url": "http://192.0.2.1/v1", "url_mode": "base",
                                   "auth": "bearer", "forward_mode": "convert", "transport": "catie_tasks", "task_api_url": "http://192.0.2.1/v1/openai/tasks",
                                   "headers": [{"header_key": "X-Acceptance", "header_value": "preserved"}]}]}}
            channel = api("/api/v1/channel/create", channel_payload)
            key_id = channel["keys"][0]["id"]
            for case in cases:
                api("/api/v1/group/create", {"name": "accept-" + case, "endpoint_type": "chat", "mode": 3,
                    "first_token_time_out": 8, "attempt_time_out": 20, "hedge_enabled": True, "hedge_delay_ms": 1,
                    "stream_keepalive_enabled": True, "relay_retry_count": 3, "relay_route_retries": 3,
                    "items": [{"channel_id": channel["id"], "model_name": "provider-full/" + case, "priority": 1, "weight": 1}]})
            client_key = api("/api/v1/apikey/create", {"name": "synthetic-client", "enabled": True})
            downstream_key = client_key["api_key"]
            assert api("/api/v1/channel/list")[0]["connection_config"] == channel_payload["connection_config"]
            checks.append("task_configuration_save_readback")
            for case in cases:
                body = {"model": "accept-" + case, "stream": case != "nonstream", "max_tokens": 32768,
                        "messages": [{"role": "user", "content": "safe synthetic HTTP acceptance"}]}
                if case == "mixed_choices":
                    body["n"] = 2
                headers = {"Content-Type": "application/json", "Authorization": "Bearer " + downstream_key}
                if case == "filter_body":
                    headers["X-Conversation-ID"] = "synthetic-replay-session"
                request = urllib.request.Request(base + "/v1/chat/completions", data=json.dumps(body).encode(),
                    headers=headers)
                with opener.open(request, timeout=12) as response:
                    request_id = response.headers["X-Request-ID"]
                    if case == "cancel":
                        response.read(1)
                        assert polled[case].wait(4), "task did not start querying"
                        # Closing the real HTTP socket transmits cancellation to Gin.
                        response.fp.raw._sock.shutdown(socket.SHUT_RDWR)
                        response.close()
                        assert cancelled[case].wait(6), "client disconnect did not cancel task"
                        before = counts[(case, "get")]
                        time.sleep(0.6)
                        assert counts[(case, "get")] == before, "polling continued after cancellation"
                        checks.append("http_disconnect_stops_polling_and_cancels_once")
                        wire = ""
                    else:
                        wire = response.read().decode()
                if case == "cancel":
                    pass
                elif case == "nonstream":
                    result = json.loads(wire)
                    assert result["choices"][0]["message"]["content"] == "safe complete"
                    checks.append("buffered_result")
                else:
                    events, done = parse_sse(wire)
                    if case in ("missing_finish", "submission_unknown"):
                        assert done == 0 and sum("error" in event for event in events) == 1
                    else:
                        assert done == 1 and not any("error" in event for event in events), case
                        terms = [c["finish_reason"] for event in events for c in event.get("choices", []) if c.get("finish_reason")]
                        expected = ["content_filter", "stop"] if case == "mixed_choices" else ["content_filter"] if case.startswith("filter") else ["length"] if case == "length" else ["stop"]
                        assert terms == expected, (case, terms)
                        text = "".join(c.get("delta", {}).get("content") or "" for event in events for c in event.get("choices", []))
                        expected_text = ordinary_text if case == "normal" else "safe firstsafe second" if case == "mixed_choices" else "" if case == "filter_empty" else "safe partial"
                        assert text == expected_text, (case, "text changed")
                    checks.append(case + "_wire")
                    if case == "filter_body":
                        replay_request = urllib.request.Request(base + "/v1/chat/completions", data=json.dumps(body).encode(), headers=headers)
                        with opener.open(replay_request, timeout=12) as replay_response:
                            replay_wire = replay_response.read().decode()
                        assert replay_wire == wire, "replay changed accepted filter events or appended error"
                        assert counts[(case, "post")] == 1, "replay created a second task"
                        checks.append("filter_replay_same_events_no_new_task")
                # The server finishes its trace after the client receives EOF.
                # Wait for that real finalization, never substitute a fixture log.
                log_deadline = time.monotonic() + 4
                selected = []
                while time.monotonic() < log_deadline:
                    logs = api("/api/v1/log/list?page_size=50&model=accept-" + case)
                    selected = [entry for entry in logs if entry.get("trace_id") == request_id]
                    if selected:
                        break
                    time.sleep(0.05)
                assert len(selected) == 1, (case, "not exactly one business log", request_id,
                    [(entry.get("trace_id"), entry.get("request_model_name")) for entry in logs])
                detail = api("/api/v1/log/detail?id=" + str(selected[0]["id"]))
                diagnostics = detail["diagnostics"]
                task = diagnostics["task"]
                assert diagnostics["selected_attempt"] == 1 and task["transport"] == "catie_tasks"
                assert diagnostics["attempts"][0]["model"] == "provider-full/" + case
                assert diagnostics["attempts"][0]["key_id"] == key_id
                assert diagnostics["outbound_budget_state"] == "set" and diagnostics["outbound_max_tokens"] == 32768
                if case not in ("submission_unknown",):
                    assert diagnostics["network"]["timing_source"] == "task_submission"
                    assert task["deadline_at"] - task["started_at"] <= 1800_000
                if case.startswith("filter") or case == "length":
                    assert detail["generation_outcome"] == "failed"
                    assert detail["termination_cause"] == ("token_limit" if case == "length" else "content_filter")
                    assert diagnostics["terminal_source"] == "upstream_terminal"
                if case == "cancel":
                    assert task["cancel_state"] == "confirmed" and detail["generation_outcome"] == "failed"
                encoded = json.dumps(diagnostics)
                assert upstream_key not in encoded and "synthetic-task-" not in encoded and "safe partial" not in encoded
                checks.append(case + "_log_and_target")
            assert not errors, errors
            for case in cases:
                assert counts[(case, "post")] == 1, (case, "submission repeated")
            assert counts[("query_retry", "get")] == 2
            assert counts[("cancel", "cancel")] == 1
            stats = api("/api/v1/apikey/stats", auth=downstream_key)["stats"]
            assert stats["request_success"] + stats["request_failed"] == len(cases), "recovery/replay doubled business requests"
            checks.append("single_submission_and_single_business_accounting")
            report = {"schema_version": 1, "source_commit": args.expected_commit, "version": args.expected_version,
                      "image": args.image, "checks": checks, "status": "passed", "synthetic": True,
                      "real_provider_contract": "not_verified", "client_saved_response": "not_verified"}
            Path(args.report).write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
            print("PASS: synthetic process HTTP acceptance (" + str(len(checks)) + " checks), source " + args.expected_commit)
        finally:
            if args.image:
                subprocess.run(["docker", "stop", "--time", "5", container_name], capture_output=True, timeout=15)
            else:
                process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
            proxy.shutdown()
            proxy.server_close()


if __name__ == "__main__":
    main()
