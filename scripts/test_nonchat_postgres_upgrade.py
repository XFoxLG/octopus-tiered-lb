"""CI-only upgrade of a synthetic v2.10.1 database backup, then old-image readback."""
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import tempfile
import time
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
        stop()
        sql(restored, "UPDATE stats_totals SET unattributed_token=1000,rerank_documents=30,rerank_documents_observed=1 WHERE id=1")
        token = start(restored, True, directory)
        data = request("/api/v1/stats/total", token=token)
        assert data["input_token"] == 451 and data["request_success"] == 1, data
        stop()
        assert sql(restored, "SELECT unattributed_token||':'||rerank_documents||':'||rerank_documents_observed FROM stats_totals WHERE id=1") == "1000:30:1"
        print("PASS: released v2.10.1 image -> database backup -> current upgrade 083 -> old image read/write; additive counters retained")
finally:
    stop()
    for database in (restored, original):
        sql(parsed.path.lstrip("/"), "DROP DATABASE IF EXISTS " + database + " WITH (FORCE)")
