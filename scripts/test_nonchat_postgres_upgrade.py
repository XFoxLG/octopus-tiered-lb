"""CI-only released database backup upgrade, 084 evidence and v2 task rollback."""
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from urllib.parse import urlsplit, urlunsplit

OLD_IMAGE = "ghcr.io/xfoxlg/octopus-tiered-lb@sha256:deda9e9f4ea70d43d0bec347f7062121e41e66aa1d15c15bc6aca4efb1b03ce3"
base = os.environ["OCTOPUS_TEST_POSTGRES_DSN"]
parsed = urlsplit(base)
if parsed.hostname not in ("localhost", "127.0.0.1"):
    raise RuntimeError("only a local CI PostgreSQL instance may be used")
binary = str(Path(sys.argv[1]).resolve())
suffix = secrets.token_hex(5)
original, restored = "nonchat_old_" + suffix, "nonchat_copy_" + suffix
port = 28083
name = "octopus-nonchat-" + suffix
password = secrets.token_urlsafe(24)
secret = secrets.token_hex(32)
process = None
log_file = None

def run(args, **kwargs):
    result = subprocess.run(args, capture_output=True, **kwargs)
    if result.returncode:
        detail = result.stderr if isinstance(result.stderr, str) else result.stderr.decode(errors="replace")
        raise RuntimeError(f"{args[0]} exited {result.returncode}: " + detail.replace(password, "[redacted]").replace(secret, "[redacted]"))
    return result

def dsn(database):
    return urlunsplit(parsed._replace(path="/" + database))

def sql(database, query):
    return run(["psql", dsn(database), "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", query], text=True).stdout.strip()

def request(path, body=None, token=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(f"http://127.0.0.1:{port}" + path, headers=headers, data=None if body is None else json.dumps(body).encode())
    with urllib.request.urlopen(req, timeout=3) as response:
        return json.load(response)["data"]

def stop():
    global process, log_file
    if process is not None:
        process.terminate()
        try:
            process.wait(timeout=15)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
        process = None
    subprocess.run(["docker", "rm", "-f", name], capture_output=True)
    if log_file:
        log_file.close()
        log_file = None

def start(database, old, directory):
    global process, log_file
    env = {"OCTOPUS_DATABASE_TYPE":"postgres", "OCTOPUS_DATABASE_PATH":dsn(database),
        "OCTOPUS_SERVER_PORT":str(port), "OCTOPUS_AUTH_JWT_SECRET":secret,
        "OCTOPUS_INITIAL_ADMIN_USERNAME":"ciadmin", "OCTOPUS_INITIAL_ADMIN_PASSWORD":password,
        "OCTOPUS_EXTERNAL_LLM_PRICE_URL":"http://127.0.0.1:1/disabled"}
    if old:
        args = ["docker", "run", "-d", "--name", name, "--network", "host"]
        for key, value in env.items():
            args.extend(["-e", key + "=" + value])
        run(args + [OLD_IMAGE])
    else:
        env = {**os.environ, **env, "OCTOPUS_DATA_DIR": str(directory / "data")}
        log_file = (directory / "current.log").open("w")
        process = subprocess.Popen([binary, "start"], env=env, cwd=directory, stdout=log_file, stderr=subprocess.STDOUT)
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        try:
            request("/api/v1/bootstrap/status")
            return request("/api/v1/user/login", {"username":"ciadmin", "password":password})["token"]
        except (OSError, ValueError, KeyError):
            time.sleep(0.25)
    if old:
        detail = subprocess.run(["docker", "logs", "--tail", "40", name], capture_output=True, text=True).stdout
    else:
        detail = (directory / "current.log").read_text()[-8000:]
    raise RuntimeError("application did not start within 60 seconds: " + detail.replace(password, "[redacted]").replace(secret, "[redacted]"))

try:
    sql(parsed.path.lstrip("/"), "CREATE DATABASE " + original)
    sql(parsed.path.lstrip("/"), "CREATE DATABASE " + restored)
    run(["docker", "pull", OLD_IMAGE])
    with tempfile.TemporaryDirectory(prefix="octopus-nonchat-ci-") as temp:
        directory = Path(temp)
        token = start(original, True, directory)
        request("/api/v1/stats/total", token=token)
        stop()
        sql(original, "INSERT INTO stats_totals (id,input_token,output_token,request_success) VALUES (1,451,0,1) ON CONFLICT (id) DO UPDATE SET input_token=451,output_token=0,request_success=1")
        # Match the CI server major; ubuntu-latest's installed client may be older.
        postgres = ["docker", "run", "--rm", "--network", "host", "-v", str(directory) + ":/backup", "postgres:17-alpine"]
        run(postgres + ["pg_dump", dsn(original), "-Fc", "-f", "/backup/synthetic.dump"])
        run(postgres + ["pg_restore", "--no-owner", "--exit-on-error", "-d", dsn(restored), "/backup/synthetic.dump"])
        token = start(restored, False, directory)
        data = request("/api/v1/stats/total", token=token)
        assert data["input_token"] == 451 and data["unattributed_token"] == 0, data
        assert sql(restored, "SELECT status FROM migration_records WHERE version=83") == "1"
        assert sql(restored, "SELECT status FROM migration_records WHERE version=84") == "1"
        # Only local synthetic objects: this does not submit a model generation.
        task_config = {"version":2,"selection":"configured","catalog":{"format":"manual"},"endpoints":[{
            "id":"task-chat","protocol":"chat","url":"https://192.0.2.1/v1","url_mode":"base",
            "auth":"bearer","forward_mode":"convert","transport":"catie_tasks","task_api_url":"https://192.0.2.1/v1/openai/tasks"}]}
        created = request("/api/v1/channel/create", {"name":"ci-tasks","type":0,"enabled":True,
            "model":"ci-task-model","connection_config":task_config,"keys":[{"enabled":True,"channel_key":"synthetic-local-only"}]}, token)
        channel_id = created["id"]
        stored_config = json.loads(sql(restored, "SELECT connection_config FROM channels WHERE id=" + str(channel_id)))
        assert stored_config == task_config, stored_config
        evidence = {"transport":"catie_tasks","source":"draft","terminal_observed":True,
            "generation_state":"completed","key_id":1,"task":{"transport":"catie_tasks","done":True,"cursor":2},
            "network":{"timing_source":"task_submission","preparation_at":1000,"response_headers_at":1001,"first_body_byte_at":1200}}
        encoded_evidence = json.dumps(evidence).replace("'", "''")
        sql(restored, "INSERT INTO channel_probe_runs(id,channel_id,model_name,key_id,started_at,created_at) VALUES(900001," + str(channel_id) + ",'ci-task-model',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)")
        sql(restored, "INSERT INTO channel_probe_results(id,run_id,channel_id,model_name,kind,item,verdict,evidence,created_at) VALUES(900001,900001," + str(channel_id) + ",'ci-task-model','capability','streaming','pass','" + encoded_evidence + "',CURRENT_TIMESTAMP)")
        # Reopen current process: config/evidence survive cache reload and restart.
        stop()
        token = start(restored, False, directory)
        channels = request("/api/v1/channel/list", token=token)
        assert any(ch["id"] == channel_id and ch["connection_config"] == task_config for ch in channels), channels
        history = request("/api/v1/channel/" + str(channel_id) + "/probe/history", token=token)
        # History intentionally lists run summaries, not result bodies.
        assert any(probe["id"] == 900001 for probe in history), history
        assert json.loads(sql(restored, "SELECT evidence FROM channel_probe_results WHERE id=900001")) == evidence
        stop()
        sql(restored, "UPDATE stats_totals SET unattributed_token=1000,rerank_documents=30,rerank_documents_observed=1 WHERE id=1")
        token = start(restored, True, directory)
        data = request("/api/v1/stats/total", token=token)
        assert data["input_token"] == 451 and data["request_success"] == 1, data
        # Old binary must reject v2 at its own configuration boundary, never
        # silently erase transport fields and turn this into direct forwarding.
        try:
            request("/api/v1/channel/update", {"id":channel_id,"connection_config":task_config}, token)
            raise AssertionError("old image accepted task configuration v2")
        except urllib.error.HTTPError as error:
            assert error.code == 400, error.code
            assert "unsupported connection config version" in error.read().decode()
        stop()
        assert sql(restored, "SELECT unattributed_token||':'||rerank_documents||':'||rerank_documents_observed FROM stats_totals WHERE id=1") == "1000:30:1"
        assert json.loads(sql(restored, "SELECT evidence FROM channel_probe_results WHERE id=900001")) == evidence
        assert json.loads(sql(restored, "SELECT connection_config FROM channels WHERE id=" + str(channel_id))) == task_config
        assert sql(restored, "SELECT COUNT(*) FROM migration_records WHERE version IN (83,84) AND status=1") == "2"
        print("PASS: released v2.10.1 -> backup copy -> upgrade 083/084 -> restart -> old-image read/write and v2 rejection; evidence and task configuration retained")
finally:
    stop()
    for database in (restored, original):
        sql(parsed.path.lstrip("/"), "DROP DATABASE IF EXISTS " + database + " WITH (FORCE)")
