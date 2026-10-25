#!/usr/bin/env python3
"""Opt-in synthetic AWS qualification. With no arguments: strictly offline plan."""
import argparse
import datetime as dt
import hashlib
import hmac
import http.client
import io
import json
import os
from pathlib import Path
import re
import selectors
import signal
import ssl
import stat
import struct
import subprocess
import sys
import time
import urllib.parse
import uuid
import xml.etree.ElementTree as ET
import zipfile

ROOT = Path(__file__).resolve().parent
TEMPLATE = ROOT / "stack.json"
OWNER_TAG = "nextgo-qualification-owner"
KIND_TAG = "nextgo-qualification"
KIND = "synthetic-v1"
SHA = re.compile(r"[a-f0-9]{64}")
STACK = re.compile(r"nextgo-qual-[a-z0-9][a-z0-9-]{0,35}")
REGION = re.compile(r"(?:us|eu|ap|ca|sa|me|af|il|mx)-(?:east|west|north|south|central|northeast|southeast)-[1-9]")
PROFILE = re.compile(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,63}")


class Refused(Exception):
    """Only constant, nonsensitive messages belong in this exception."""


def require(value, message):
    if not value:
        raise Refused(message)


def digest(value):
    return hashlib.sha256(value).hexdigest()


def offline_plan():
    return {
        "status": "offline-plan-only", "cloudCalls": 0,
        "templateSHA256": digest(TEMPLATE.read_bytes()),
        "scaffoldSHA256": digest(Path(__file__).read_bytes()),
        "actions": ["run --execute: verify explicit profile/account/principal; create owned staging stack; upload one frozen ZIP; update same frozen template; verify exported signing identity; sign 14 synthetic HTTPS GETs; delete owned resources",
                    "cleanup --execute: verify account, exact stack ARN and ownership tags; delete only inspected artifact object and owned stack"],
        "resources": "2 private AWS_IAM Function URLs, provided.al2023, 512 MiB, 10 s, reserved concurrency 1 each; private encrypted S3; log-only execution role; 1-day logs",
        "limits": {"cloudCLICommands": 180, "httpRequests": 15, "syntheticRequests": 14, "signingIdentityRequests": 1, "rawResponseBytes": 8388608, "defaultWorkMinutes": 15, "cleanupExtraMinutes": 5},
        "approvalStillRequired": ["account", "region", "named CLI profile and invoker ARN", "synthetic ZIP and digest", "resource/charge budget"],
        "warning": "Budget acknowledgement is not an AWS billing cap. No AWS qualification has been executed by preparing this scaffold."
    }


def validate_options(args):
    require(args.execute, "Cloud actions require explicit --execute")
    require(args.account and re.fullmatch(r"[0-9]{12}", args.account), "Explicit 12-digit expected account required")
    require(args.region and REGION.fullmatch(args.region), "Explicit standard commercial AWS region required")
    require(args.profile and PROFILE.fullmatch(args.profile), "Explicit safe named CLI profile required")
    require(args.budget_usd is not None and 0 < args.budget_usd <= 25, "Explicit approved budget acknowledgement (0 < USD <= 25) required")
    require(not (args.action == "cleanup" and args.keep), "cleanup cannot be combined with --keep")
    require(os.name == "posix" and hasattr(signal, "setitimer"), "Cloud execution requires POSIX Linux/macOS")


def inspect_zip(path, expected_sha, architecture):
    require(SHA.fullmatch(expected_sha or ""), "Explicit ZIP SHA-256 required")
    fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    with os.fdopen(fd, "rb") as stream:
        require(stat.S_ISREG(os.fstat(stream.fileno()).st_mode), "ZIP must be a regular file")
        encoded = stream.read((50 << 20) + 1)
    require(len(encoded) <= 50 << 20, "ZIP must be <= 50 MiB")
    require(digest(encoded) == expected_sha, "ZIP digest mismatch")
    # Parse exactly the bytes whose digest was approved, never reopen the path.
    with zipfile.ZipFile(io.BytesIO(encoded)) as archive:
        names = archive.infolist()
        require(len(names) == 1 and names[0].filename == "bootstrap", "ZIP must contain only root bootstrap")
        entry = names[0]
        mode = entry.external_attr >> 16
        require(stat.S_ISREG(mode) and mode & 0o111 and entry.file_size <= 100 << 20, "bootstrap must be a regular executable <= 100 MiB")
        body = archive.read(entry)
    require(len(body) >= 64 and body[:6] == b"\x7fELF\x02\x01", "Require an existing little-endian 64-bit Linux ELF bootstrap")
    require(struct.unpack_from("<H", body, 18)[0] == {"x86_64": 62, "arm64": 183}[architecture], "ELF architecture mismatch")
    return digest(body), encoded


def freeze_artifact(path, encoded):
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
    fd = os.open(path, flags, 0o400)
    with os.fdopen(fd, "wb") as stream:
        stream.write(encoded)
        stream.flush()
        os.fsync(stream.fileno())


def safe_environment(source):
    # Examine names only. Never inspect or print ambient credential values.
    denied = {"AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE", "AWS_CA_BUNDLE", "AWS_WEB_IDENTITY_TOKEN_FILE",
              "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "SSL_CERT_FILE", "SSL_CERT_DIR",
              "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "BOTO_CONFIG", "SSLKEYLOGFILE"}
    require(not any(key in denied or key.startswith("AWS_ENDPOINT_URL") or key.startswith("AWS_CONTAINER_AUTHORIZATION") for key in source), "Custom credential/endpoint/CA environment is unsupported")
    env = {key: source[key] for key in source if not key.startswith("AWS_")}
    env.update(AWS_EC2_METADATA_DISABLED="true", AWS_IGNORE_CONFIGURED_ENDPOINT_URLS="true", AWS_MAX_ATTEMPTS="1",
               AWS_RETRY_MODE="standard", AWS_PAGER="", AWS_CLI_AUTO_PROMPT="off", AWS_STS_REGIONAL_ENDPOINTS="regional")
    return env


class AWS:
    def __init__(self, args):
        self.args, self.env, self.calls = args, safe_environment(os.environ), 0
        self.call_limit = 130  # reserve 50 commands for ownership checks/deletion
        self.deadline = time.monotonic() + args.max_minutes * 60

    def command(self, parts, *, missing=False, raw=False):
        self.calls += 1
        require(self.calls <= self.call_limit and time.monotonic() < self.deadline, "Cloud command/time budget exhausted")
        command = ["aws", *parts, "--profile", self.args.profile, "--region", self.args.region,
                   "--no-cli-pager", "--no-cli-auto-prompt", "--cli-connect-timeout", "5", "--cli-read-timeout", "20"]
        if parts[0] != "configure":
            service = "s3" if parts[0] == "s3api" else parts[0]
            command += ["--endpoint-url", f"https://{service}.{self.args.region}.amazonaws.com", "--output", "json", "--no-paginate"]
        process = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=self.env)
        selector, output, sizes = selectors.DefaultSelector(), bytearray(), {}
        for pipe in (process.stdout, process.stderr):
            selector.register(pipe, selectors.EVENT_READ)
            sizes[pipe] = 0
        end = min(self.deadline, time.monotonic() + 30)
        try:
            while selector.get_map():
                require(time.monotonic() < end, "AWS CLI command deadline exceeded")
                for key, _ in selector.select(min(0.25, max(0, end - time.monotonic()))):
                    data = os.read(key.fileobj.fileno(), 4096)
                    if not data:
                        selector.unregister(key.fileobj)
                        continue
                    sizes[key.fileobj] += len(data)
                    require(sizes[key.fileobj] <= 65536, "AWS CLI output bound exceeded")
                    if key.fileobj is process.stdout:
                        output.extend(data)
            code = process.wait(timeout=max(0.1, end - time.monotonic()))
        finally:
            selector.close()
            if process.poll() is None:
                process.kill()
                process.wait(timeout=5)
            process.stdout.close()
            process.stderr.close()
        if code and not missing:
            raise Refused("AWS CLI operation failed; raw output intentionally not retained")
        if code:
            return None
        if raw:
            return bytes(output).decode("utf-8").strip()
        return json.loads(output) if output.strip() else {}

    def preflight(self):
        # Restrict this small scaffold to named static/preauthenticated profiles
        # or a bounded source_profile chain. No custom processes, SSO discovery,
        # metadata, web identity, alternate endpoints, or alternate CA bundles.
        profile, seen = self.args.profile, set()
        for _ in range(4):
            require(PROFILE.fullmatch(profile) and profile not in seen, "Unsafe profile chain")
            seen.add(profile)
            original = self.args.profile
            self.args.profile = profile
            try:
                for key in ("endpoint_url", "services", "ca_bundle", "credential_process", "credential_source", "web_identity_token_file", "sso_session", "sso_start_url"):
                    require(not self.command(["configure", "get", key], missing=True, raw=True), "Unsupported profile credential/endpoint/CA setting")
                following = self.command(["configure", "get", "source_profile"], missing=True, raw=True)
            finally:
                self.args.profile = original
            if not following:
                break
            profile = following
        else:
            raise Refused("Profile chain too deep")
        identity = self.command(["sts", "get-caller-identity"])
        require(identity.get("Account") == self.args.account, "Expected account mismatch; no provisioning allowed")
        if self.args.action != "cleanup":
            require(principal_matches(self.args.invoker_arn, identity.get("Arn", "")), "Profile does not match the exact approved invoker principal")


def principal_matches(expected, actual):
    if not isinstance(expected, str):
        return False
    if re.fullmatch(r"arn:aws:iam::[0-9]{12}:user/[A-Za-z0-9+=,.@_/-]+", expected):
        return expected == actual
    role = re.fullmatch(r"arn:aws:iam::([0-9]{12}):role/([A-Za-z0-9+=,.@_-]+)", expected)
    if not role:
        return False  # Role paths are deliberately unsupported in this scaffold.
    return bool(re.fullmatch(r"arn:aws:sts::" + role[1] + r":assumed-role/" + re.escape(role[2]) + r"/[A-Za-z0-9+=,.@_-]+", actual))


def owned_stack(description, state):
    require(len(description.get("Stacks", [])) == 1, "Exact owned stack not found")
    stack = description["Stacks"][0]
    arn_prefix = f"arn:aws:cloudformation:{state['region']}:{state['account']}:stack/{state['stack']}/"
    require(stack.get("StackId", "").startswith(arn_prefix), "Stack ARN/account/region mismatch")
    if state.get("stackArn"):
        require(stack["StackId"] == state["stackArn"], "Stack identity changed")
    tags = {tag["Key"]: tag["Value"] for tag in stack.get("Tags", [])}
    require(tags.get(OWNER_TAG) == state["owner"] and tags.get(KIND_TAG) == KIND, "Stack ownership tags mismatch; deletion refused")
    require(stack.get("StackName") == state["stack"], "Stack name mismatch")
    return stack


def stack_description(aws, state):
    return owned_stack(aws.command(["cloudformation", "describe-stacks", "--stack-name", state.get("stackArn", state["stack"])]), state)


def wait_stack(aws, state, expected):
    for _ in range(40):
        value = stack_description(aws, state)
        status = value["StackStatus"]
        if status == expected:
            return value
        require(status in ("CREATE_IN_PROGRESS", "UPDATE_IN_PROGRESS", "UPDATE_COMPLETE_CLEANUP_IN_PROGRESS"), "Stack did not reach expected state")
        time.sleep(4)
    raise Refused("CloudFormation polling budget exhausted")


def write_private(path, value):
    temporary = path.with_name(path.name + "." + uuid.uuid4().hex + ".next")
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
    fd = os.open(temporary, flags, 0o600)
    try:
        with os.fdopen(fd, "w") as stream:
            json.dump(value, stream, indent=2)
            stream.write("\n")
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def parameters(state, deploy):
    return [{"ParameterKey": key, "ParameterValue": value} for key, value in {
        "Owner": state["owner"], "DeployFunctions": str(deploy).lower(), "ArtifactKey": state["key"],
        "Architecture": state["architecture"], "InvokerArn": state["invokerArn"]}.items()]


def cleanup(aws, state):
    # No wildcard deletion, name-prefix search, recursive bucket removal, or
    # ownership adoption. Foreign objects deliberately block stack deletion.
    stack_description(aws, state)
    resource = aws.command(["cloudformation", "describe-stack-resource", "--stack-name", state["stackArn"], "--logical-resource-id", "StagingBucket"])
    bucket = resource["StackResourceDetail"]["PhysicalResourceId"]
    require(re.fullmatch(r"[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]", bucket), "Unexpected owned bucket name")
    listing = aws.command(["s3api", "list-objects-v2", "--bucket", bucket, "--max-keys", "2"])
    objects = listing.get("Contents", [])
    require(not listing.get("IsTruncated") and all(item["Key"] == state["key"] for item in objects), "Foreign bucket content; automatic cleanup refused")
    if objects:
        head = aws.command(["s3api", "head-object", "--bucket", bucket, "--key", state["key"]])
        require(head.get("Metadata", {}).get("qualification-owner") == state["owner"] and head["Metadata"].get("artifact-sha256") == state["zipSHA256"], "Artifact ownership metadata mismatch")
        aws.command(["s3api", "delete-object", "--bucket", bucket, "--key", state["key"]])
    aws.command(["cloudformation", "delete-stack", "--stack-name", state["stackArn"]])
    # Describe by exact ARN; deleted stacks remain queryable for a time.
    for _ in range(40):
        status = stack_description(aws, state)["StackStatus"]
        if status == "DELETE_COMPLETE":
            return
        require(status == "DELETE_IN_PROGRESS", "Owned stack cleanup incomplete")
        time.sleep(4)
    raise Refused("Cleanup polling exhausted; retain state and inspect owned stack")


def signed_headers(credentials, host, region, path, headers, now=None, service="lambda"):
    now = now or dt.datetime.now(dt.timezone.utc)
    stamp, day = now.strftime("%Y%m%dT%H%M%SZ"), now.strftime("%Y%m%d")
    empty = digest(b"")
    values = {key.lower(): value for key, value in headers.items()}
    values.update(host=host, **{"x-amz-date": stamp, "x-amz-content-sha256": empty})
    if credentials.get("SessionToken"):
        values["x-amz-security-token"] = credentials["SessionToken"]
    url = urllib.parse.urlsplit(path)
    quote = lambda value: urllib.parse.quote(value, safe="-_.~")
    query = "&".join(f"{quote(key)}={quote(value)}" for key, value in sorted(urllib.parse.parse_qsl(url.query, keep_blank_values=True)))
    names = ";".join(sorted(values))
    canonical = "GET\n" + urllib.parse.quote(urllib.parse.unquote(url.path), safe="/-_.~") + "\n" + query + "\n" + "".join(key + ":" + values[key].strip() + "\n" for key in sorted(values)) + "\n" + names + "\n" + empty
    scope = f"{day}/{region}/{service}/aws4_request"
    signing = ("AWS4" + credentials["SecretAccessKey"]).encode()
    for part in (day, region, service, "aws4_request"):
        signing = hmac.new(signing, part.encode(), hashlib.sha256).digest()
    signature = hmac.new(signing, ("AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + digest(canonical.encode())).encode(), hashlib.sha256).hexdigest()
    values["authorization"] = f"AWS4-HMAC-SHA256 Credential={credentials['AccessKeyId']}/{scope}, SignedHeaders={names}, Signature={signature}"
    return values


def https_get(url, region, credentials, path, extra=None, *, deadline, service="lambda"):
    parsed = urllib.parse.urlsplit(url)
    destination = re.fullmatch(r"[a-z0-9]+\.lambda-url\." + re.escape(region) + r"\.on\.aws", parsed.netloc) if service == "lambda" else service == "sts" and parsed.netloc == f"sts.{region}.amazonaws.com"
    require(destination and parsed.scheme == "https" and parsed.path == "/" and not parsed.query and not parsed.fragment, "Unexpected AWS HTTPS destination")
    def remaining():
        value = deadline - time.monotonic()
        require(value > 0, "Approved global HTTPS deadline exhausted")
        return min(5, value)
    headers = {"accept-encoding": "gzip", **(extra or {})}
    connection = http.client.HTTPSConnection(parsed.netloc, timeout=remaining(), context=ssl.create_default_context())
    start, body, loading, loaded = time.monotonic(), bytearray(), None, None
    try:
        connection.request("GET", path, headers=signed_headers(credentials, parsed.netloc, region, path, headers, service=service))
        connection.sock.settimeout(remaining())
        response = connection.getresponse()
        first = None
        while True:
            require(time.monotonic() - start < 12, "HTTPS request deadline exceeded")
            timeout = remaining()
            if connection.sock is not None:
                connection.sock.settimeout(timeout)
            chunk = response.read1(16384)
            if not chunk:
                break
            elapsed = round((time.monotonic() - start) * 1000)
            first = elapsed if first is None else first
            require(len(body) + len(chunk) <= 8 << 20, "HTTPS response bound exceeded")
            body.extend(chunk)
            if b"data-stream-loading" in body and loading is None:
                loading = elapsed
            if b"data-stream-loaded" in body and loaded is None:
                loaded = elapsed
        raw = bytes(body)
        # No redirects are followed and no response body, cookie, auth header,
        # account identifier, or endpoint hostname enters retained proof.
        record = {"path": path, "hostSHA256": digest(parsed.netloc.encode()), "status": response.status, "mime": response.getheader("Content-Type", "").split(";")[0], "bodySHA256": digest(raw), "wireBytes": len(raw), "firstByteMs": first, "completeMs": round((time.monotonic() - start) * 1000), "loadingMs": loading, "loadedMs": loaded}
        if response.getheader("Content-Encoding") == "gzip":
            import zlib
            decoder = zlib.decompressobj(31)
            raw = decoder.decompress(raw, (32 << 20) + 1)
            require(len(raw) <= 32 << 20 and decoder.eof and not decoder.unconsumed_tail, "Decoded body bound or encoding failure")
        return record, raw, response
    finally:
        connection.close()


def exercise(aws, state, proof):
    stack = stack_description(aws, state)
    outputs = {item["OutputKey"]: item["OutputValue"] for item in stack.get("Outputs", [])}
    # This export happens only within explicitly invoked run/exercise. Secrets
    # stay in process memory and signed TLS headers, never argv/files/proof.
    credentials = aws.command(["configure", "export-credentials", "--format", "process"])
    require(all(isinstance(credentials.get(key), str) and 16 <= len(credentials[key]) <= 4096 for key in ("AccessKeyId", "SecretAccessKey")), "Approved profile did not export usable credentials")
    # Bind the actual exported key, not just an earlier profile lookup, to the
    # approved principal. No ambient credentials or bearer header are reused.
    identity_record, identity_body, _ = https_get(f"https://sts.{state['region']}.amazonaws.com/", state["region"], credentials,
        "/?Action=GetCallerIdentity&Version=2011-06-15", deadline=aws.deadline, service="sts")
    proof["signingIdentity"] = identity_record
    require(identity_record["status"] == 200 and len(identity_body) <= 65536, "Signing identity verification failed")
    identity = ET.fromstring(identity_body)
    require(identity.findtext(".//{*}Account") == state["account"] and principal_matches(state["invokerArn"], identity.findtext(".//{*}Arn", "")), "Exported signing credentials do not match approved invoker")
    for mode in ("Buffered", "Streaming"):
        target = outputs[mode + "URL"]
        build = configuration = None
        for path in ("/api/health", "/resources", "/resources?transport=aws", "/api/resources", "/api/social?title=AWS%20synthetic&description=Public%20qualification", "/stream"):
            headers = {"x-nextgo-navigate": "1"} if "transport=aws" in path else {}
            if path == "/stream":
                headers["accept-encoding"] = "identity"
            record, body, response = https_get(target, state["region"], credentials, path, headers, deadline=aws.deadline)
            record["mode"] = mode.lower()
            proof["requests"].append(record)
            require(record["status"] == 200, "Function URL response was not successful")
            if path == "/api/health":
                require(json.loads(body).get("ok") is True, "Synthetic health contract failed")
            elif path == "/resources":
                require(b"Public Go resource" in body and response.getheader("X-Nextgo-Requires-Server") == "1", "Synthetic SSR contract failed")
                build = re.search(rb'data-nextgo-build="([a-f0-9]+)"', body)[1].decode()
                configuration = re.search(rb'data-nextgo-configuration="([a-f0-9]{64})"', body)[1].decode()
            elif "transport=aws" in path:
                page = json.loads(body)
                require(page.get("version") == 3 and page.get("build") == build and page.get("configuration") == configuration and "Public Go resource" in page.get("html", ""), "Navigation identity contract failed")
            elif path == "/api/resources":
                require(json.loads(body).get("message") == "Public Go resource", "Public JSON contract failed")
            elif path.startswith("/api/social"):
                require(body[:8] == b"\x89PNG\r\n\x1a\n" and struct.unpack_from(">II", body, 16) == (1200, 630), "PNG contract failed")
            elif path == "/stream":
                require(b"Deferred content is ready" in body, "Deferred content missing")
                if mode == "Streaming":
                    require(record["loadingMs"] is not None and record["loadedMs"] is not None and record["loadedMs"] - record["loadingMs"] >= 100, "Real Function URL did not expose separate early/deferred publication")
        record, body, _ = https_get(target, state["region"], credentials, f"/_nextgo/{build}/app.wasm", deadline=aws.deadline)
        record["mode"] = mode.lower()
        proof["requests"].append(record)
        require(record["status"] == 200, "WASM Function URL response was not successful")
        require(body[:8] == b"\0asm\x01\0\0\0", "WASM asset transport failed")


def provision(aws, args, state, save):
    require(not aws.command(["cloudformation", "describe-stacks", "--stack-name", state["stack"]], missing=True), "Stack name already exists; never adopt or update it")
    params = json.dumps(parameters(state, False), separators=(",", ":"))
    template = args.out / "stack.json"
    require(not template.is_symlink() and digest(template.read_bytes()) == state["templateSHA256"], "Frozen template identity mismatch")
    result = aws.command(["cloudformation", "create-stack", "--stack-name", state["stack"], "--template-body", "file://" + str(template.resolve()), "--parameters", params,
                          "--capabilities", "CAPABILITY_IAM", "--tags", f"Key={OWNER_TAG},Value={state['owner']}", f"Key={KIND_TAG},Value={KIND}", "--timeout-in-minutes", "5"])
    state["stackArn"] = result["StackId"]
    save()
    stack = wait_stack(aws, state, "CREATE_COMPLETE")
    bucket = next(item["OutputValue"] for item in stack["Outputs"] if item["OutputKey"] == "Bucket")
    artifact = args.out / "artifact.zip"
    require(not artifact.is_symlink() and artifact.stat().st_mode & 0o377 == 0, "Private artifact snapshot permissions changed")
    inspect_zip(artifact, state["zipSHA256"], state["architecture"])
    aws.command(["s3api", "put-object", "--bucket", bucket, "--key", state["key"], "--body", str(artifact.resolve()), "--server-side-encryption", "AES256",
                 "--metadata", json.dumps({"qualification-owner": state["owner"], "artifact-sha256": state["zipSHA256"]})])
    aws.command(["cloudformation", "update-stack", "--stack-name", state["stackArn"], "--template-body", "file://" + str(template.resolve()), "--parameters", json.dumps(parameters(state, True)), "--capabilities", "CAPABILITY_IAM"])
    wait_stack(aws, state, "UPDATE_COMPLETE")


def load_state(path, args):
    require(path.is_file() and not path.is_symlink() and path.stat().st_size <= 16384, "Private state file required")
    require(path.stat().st_mode & 0o077 == 0, "State permissions must be private")
    require(not hasattr(os, "getuid") or path.stat().st_uid == os.getuid(), "State must belong to the current user")
    state = json.loads(path.read_text())
    require(state.get("version") == 1 and STACK.fullmatch(state.get("stack", "")) and re.fullmatch(r"[a-f0-9]{32}", state.get("owner", "")), "Invalid owned state")
    require(state.get("account") == args.account and state.get("region") == args.region, "State account/region mismatch")
    require(SHA.fullmatch(state.get("zipSHA256", "")) and state.get("key") == f"artifacts/{state['owner']}/bootstrap-{state['zipSHA256']}.zip", "State artifact binding mismatch")
    require(state.get("templateSHA256") == digest(TEMPLATE.read_bytes()), "State template changed; manual review required")
    return state


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("plan", "run", "exercise", "cleanup"), nargs="?", default="plan")
    parser.add_argument("--execute", action="store_true")
    for name in ("profile", "account", "region", "stack", "invoker-arn", "zip-sha256"):
        parser.add_argument("--" + name)
    parser.add_argument("--budget-usd", type=float)
    parser.add_argument("--max-minutes", type=int, choices=range(5, 21), default=15)
    parser.add_argument("--architecture", choices=("x86_64", "arm64"), default="x86_64")
    parser.add_argument("--zip", type=Path)
    parser.add_argument("--out", type=Path)
    parser.add_argument("--state", type=Path)
    parser.add_argument("--keep", action="store_true", help="Intentionally retain owned resources, including on failure; charges continue")
    args = parser.parse_args(argv)
    if args.action == "plan":
        print(json.dumps(offline_plan(), indent=2))
        return 0
    validate_options(args)
    if args.action == "run":
        require(args.stack and STACK.fullmatch(args.stack), "Unique explicit nextgo-qual-* stack required")
        require(args.invoker_arn and re.fullmatch(r"arn:aws:iam::" + args.account + r":(?:role/[A-Za-z0-9+=,.@_-]+|user/[A-Za-z0-9+=,.@_/-]+)", args.invoker_arn), "Same-account IAM user or pathless role invoker ARN required")
        require(args.zip is not None and args.out is not None, "Explicit prebuilt ZIP and new output directory required")
        elf, encoded = inspect_zip(args.zip, args.zip_sha256, args.architecture)
        owner = uuid.uuid4().hex
        template_bytes = TEMPLATE.read_bytes()
        require(len(template_bytes) <= 51200, "Template exceeds inline CloudFormation bound")
        state = {"version": 1, "owner": owner, "stack": args.stack, "account": args.account, "region": args.region, "architecture": args.architecture,
                 "invokerArn": args.invoker_arn, "zipSHA256": args.zip_sha256, "elfSHA256": elf, "templateSHA256": digest(template_bytes), "key": f"artifacts/{owner}/bootstrap-{args.zip_sha256}.zip"}
        args.out.mkdir(mode=0o700, parents=False, exist_ok=False)
        freeze_artifact(args.out / "artifact.zip", encoded)
        freeze_artifact(args.out / "stack.json", template_bytes)
        del encoded
        state_path = args.out / "state.json"
        write_private(state_path, state)
    else:
        require(args.state is not None, "Explicit owned state path required")
        state_path = args.state
        state = load_state(state_path, args)
        args.invoker_arn = state.get("invokerArn")
    proof_path = state_path.with_name("proof.json")
    proof = {"schema": "nextgo-aws-qualification/v1", "status": "incomplete", "accountSHA256": digest(args.account.encode()), "stackNameSHA256": digest(state["stack"].encode()), "region": args.region,
             "zipSHA256": state["zipSHA256"], "elfSHA256": state.get("elfSHA256"), "templateSHA256": state["templateSHA256"], "scaffoldSHA256": digest(Path(__file__).read_bytes()), "approvedBudgetUSD": args.budget_usd,
             "requests": [], "cleanup": "not-started", "limitations": "Synthetic Function URL checks only; no real-browser, ingress/CDN, operational soak, HA or production certification."}
    aws, approved_account, failure = None, False, None
    save = lambda: write_private(state_path, state)
    def retain_proof():
        nonlocal failure
        try:
            write_private(proof_path, proof)
        except OSError:
            failure = failure or Refused("Proof persistence failed; inspect owned state")
            proof["status"] = "failed"
            print("Proof persistence failed; owned cleanup will still be attempted", file=sys.stderr)
    def interrupted(_signum, _frame):
        raise KeyboardInterrupt
    previous_term = signal.signal(signal.SIGTERM, interrupted)
    previous_alarm = signal.signal(signal.SIGALRM, interrupted)
    previous_timer = signal.setitimer(signal.ITIMER_REAL, args.max_minutes * 60)
    try:
        aws = AWS(args)
        aws.preflight()
        approved_account = True
        if args.action == "run":
            # Ownership tags, never a name prefix, decide any failure cleanup.
            provision(aws, args, state, save)
        if args.action != "cleanup":
            exercise(aws, state, proof)
        proof["status"] = "passed" if args.action != "cleanup" else "cleanup-requested"
    except (Exception, KeyboardInterrupt) as error:
        failure = error
        proof["status"] = "failed"
        proof["error"] = str(error) if isinstance(error, Refused) else "Qualification failed; raw exception intentionally omitted"
    finally:
        # Preserve redacted partial proof before attempting destructive cleanup.
        retain_proof()
        if not approved_account:
            proof["cleanup"] = "preflight-refused; no cleanup attempted"
        elif not args.keep:
            try:
                aws.deadline = time.monotonic() + 300
                aws.call_limit = 180
                signal.setitimer(signal.ITIMER_REAL, 300)
                if not state.get("stackArn"):
                    candidate = aws.command(["cloudformation", "describe-stacks", "--stack-name", state["stack"]], missing=True)
                    if candidate:
                        state["stackArn"] = owned_stack(candidate, state)["StackId"]
                        try:
                            save()
                        except OSError:
                            failure = failure or Refused("State persistence failed; inspect owned state")
                            proof["status"] = "failed"
                if state.get("stackArn"):
                    cleanup(aws, state)
                    proof["cleanup"] = "deleted-owned-stack"
                else:
                    proof["cleanup"] = "no-owned-stack-confirmed"
                    if args.action != "run":
                        raise Refused("Owned stack identity could not be established")
            except (Exception, KeyboardInterrupt):
                proof["cleanup"] = "incomplete-refused-or-failed; inspect exact owned state before retry"
                failure = failure or Refused("Owned cleanup incomplete; charges may continue")
                proof["status"] = "failed"
        else:
            proof["cleanup"] = "explicitly-kept; charges continue"
        proof["cloudCLICommands"] = aws.calls if aws else 0
        retain_proof()
        signal.setitimer(signal.ITIMER_REAL, *previous_timer)
        signal.signal(signal.SIGALRM, previous_alarm)
        signal.signal(signal.SIGTERM, previous_term)
    print(json.dumps({"status": proof["status"], "cleanup": proof["cleanup"], "proof": str(proof_path), "state": str(state_path)}))
    return 1 if failure else 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Refused as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(2)
