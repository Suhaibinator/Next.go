#!/usr/bin/python3
"""Bounded hosted compile-only debug profile. Never executes produced artifacts.

The SDK installation and action infrastructure are trusted for this debug probe.
Tool receipts cover Go's toolexec boundary, not kernel-wide process/network audit.
Original SDK archive provenance and native/browser behavior remain unqualified.
"""
import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import secrets
import selectors
import shutil
import signal
import stat
import subprocess
import sys
import time

CONTRACT_SHA256 = "34f1aca9b0199534b7a8f4d32755d8d997e287a995eaf01d35403cdd4f76dc67"
CONTRACT_CANONICAL_SHA256 = "3b300d381e936c2b95b5a3b46f868cedce2f03705b2726f275d1f0d17ea57f0b"
BASE = "3d583dc7ff784ac7502449730bde091ebf1f808c"
TREE = "ba24a854fab69ee810394bc193507ba22f2a953e"
MODULE = "github.com/Suhaibinator/Next.go"
SDK_VERSIONS = ("1.24.13", "1.27.3")
DISK_LIMIT = 8 << 30
FILE_LIMIT = 16 << 20
SOURCE_LIMIT = 128 << 20
RECEIPT_LIMIT = 512
COLLECT_LIMIT = 32 << 20
UPLOAD_FILE_LIMIT = 4 << 20
METADATA = ("source-identity.json", "toolchain.json", "environment.json", "graphs.json",
            "source-selection.json", "command-proofs.json", "artifact-or-execution-receipts.json",
            "summary.json")
TOOLS = frozenset(("compile", "link", "asm", "pack", "buildid", "vet"))
COMPILE_IDS = ('compile-native-client', 'compile-native-mutation', 'compile-native-offline', 'compile-native-offline-worker', 'compile-native-internal-offlinejson', 'compile-native-ui', 'compile-native-internal-project', 'compile-cli', 'compile-core-client', 'compile-core-mutation', 'compile-core-offline', 'compile-core-offline-worker', 'compile-core-internal-offlinejson', 'compile-core-ui', 'compile-core-offlinecontrols', 'compile-devcore-client', 'compile-devcore-mutation', 'compile-devcore-ui', 'compile-devcore-offlinecontrols', 'compile-module-client', 'compile-module-ui', 'compile-module-offlinecontrols', 'compile-devmodule-client', 'compile-devmodule-ui', 'compile-devmodule-offlinecontrols')
COMMAND_IDS = ('go-version', 'go-environment', 'graph-native', 'source-selection-native', 'graph-core', 'source-selection-core', 'graph-devcore', 'source-selection-devcore', 'graph-module', 'source-selection-module', 'graph-devmodule', 'source-selection-devmodule', 'graph-cli', 'compile-native-client', 'compile-native-mutation', 'compile-native-offline', 'compile-native-offline-worker', 'compile-native-internal-offlinejson', 'compile-native-ui', 'compile-native-internal-project', 'compile-cli', 'compile-core-client', 'compile-core-mutation', 'compile-core-offline', 'compile-core-offline-worker', 'compile-core-internal-offlinejson', 'compile-core-ui', 'compile-core-offlinecontrols', 'compile-devcore-client', 'compile-devcore-mutation', 'compile-devcore-ui', 'compile-devcore-offlinecontrols', 'compile-module-client', 'compile-module-ui', 'compile-module-offlinecontrols', 'compile-devmodule-client', 'compile-devmodule-ui', 'compile-devmodule-offlinecontrols')


class Refusal(Exception):
    pass


def require(ok, message):
    if not ok:
        raise Refusal(message)


def encode(value):
    return (json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n").encode()


def safe_rel(value):
    require(isinstance(value, str), "relative path must be text")
    p = PurePosixPath(value)
    require(isinstance(value, str) and value and value != "." and "\\" not in value and not p.is_absolute()
            and str(p) == value and ".." not in p.parts and len(p.parts) <= 24
            and "\x00" not in value and "\n" not in value, "unsafe relative path")
    return p


def regular(path, cap=FILE_LIMIT):
    s = path.lstat()
    require(stat.S_ISREG(s.st_mode) and s.st_nlink == 1 and s.st_size <= cap,
            "nonregular, linked, or oversized file")
    return s


def digest_file(path, cap=FILE_LIMIT):
    before = regular(path, cap)
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(65536), b""):
            h.update(block)
    after = path.lstat()
    require((before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns) ==
            (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns), "file changed while hashing")
    return {"bytes": before.st_size, "sha256": h.hexdigest(), "dev": before.st_dev,
            "inode": before.st_ino, "mode": stat.S_IMODE(before.st_mode)}


def read_json(path, cap=1 << 20):
    regular(path, cap)
    return json.loads(path.read_bytes())


def write_json(path, value, cap=4 << 20):
    data = encode(value)
    require(len(data) <= cap, "JSON bound")
    tmp = path.with_name(path.name + ".new")
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "wb") as out:
        out.write(data)
    os.replace(tmp, path)


def contract(path):
    require(digest_file(path, 1 << 20)["sha256"] == CONTRACT_SHA256, "unreviewed contract bytes")
    c = read_json(path)
    require(c["version"] == 1 and c["baseCommit"] == BASE and c["sourceTree"] == TREE,
            "unknown contract")
    require(len(c["overlay"]) == 37 and len(c["commands"]) == 25 and len(c["metadataCommands"]) == 13,
            "contract command/source count")
    require(c["gate"] == "A_COMPILE_ONLY" and c["GateBImplemented"] is False, "wrong gate")
    return c


def verify_source(dest):
    files = {}
    for folder, dirs, names in os.walk(dest, followlinks=False):
        for name in dirs:
            require(not (Path(folder) / name).is_symlink(), "source directory symlink")
        for name in names:
            p = Path(folder) / name
            relative = str(p.relative_to(dest))
            safe_rel(relative)
            s = regular(p)
            files[relative] = ("100755" if s.st_mode & 0o111 else "100644", p.read_bytes())
            require(len(files) <= 1769, "source count changed")
    require(len(files) == 1769 and sum(len(x[1]) for x in files.values()) <= SOURCE_LIMIT
            and tree_id(files) == TREE, "materialized source changed")
    return True


def plain_env():
    return {"PATH": "/usr/bin:/bin", "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8",
            "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": "/dev/null",
            "GIT_NO_REPLACE_OBJECTS": "1", "GIT_TERMINAL_PROMPT": "0"}


def git_bytes(repo, args, cap, input_data=None, root=None):
    # Only immutable object reads: no checkout, filters, hooks, fetch, or credentials.
    argv = ["/usr/bin/git", "--no-pager", "-C", str(repo)] + args
    require(root is not None, "Git read needs owned launch journal")
    o = owner(root)
    deadline = min(time.monotonic() + 60, time.monotonic() + o["jobStart"] + 5400 - time.time())
    require(time.monotonic() < deadline, "Git read whole-job deadline")
    write_json(root / "active.json", {"phase": "GIT_STARTING_UNKNOWN", "argv": argv, "pid": None})
    p = None
    sel = None
    out, err = bytearray(), bytearray()
    try:
        p = subprocess.Popen(argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                             env=plain_env(), start_new_session=True)
        write_json(root / "active.json", {"phase": "GIT_RUNNING", "argv": argv, "pid": p.pid})
        sel = selectors.DefaultSelector()
        for stream, key in ((p.stdout, "out"), (p.stderr, "err")):
            os.set_blocking(stream.fileno(), False)
            sel.register(stream, selectors.EVENT_READ, key)
        data = memoryview(input_data or b"")
        written = 0
        if data:
            os.set_blocking(p.stdin.fileno(), False)
            sel.register(p.stdin, selectors.EVENT_WRITE, "in")
        else:
            p.stdin.close()
        while sel.get_map() or p.poll() is None:
            require(time.monotonic() < deadline, "Git read deadline")
            for key, _ in sel.select(0.05):
                stream = key.fileobj
                if key.data == "in":
                    written += os.write(stream.fileno(), data[written:written + 65536])
                    if written == len(data):
                        sel.unregister(stream)
                        stream.close()
                else:
                    block = os.read(stream.fileno(), 65536)
                    if not block:
                        sel.unregister(stream)
                        continue
                    target, limit = (out, cap) if key.data == "out" else (err, 65536)
                    require(len(target) + len(block) <= limit, "Git output bound")
                    target.extend(block)
        p.wait(timeout=5)
        require(p.returncode == 0 and process_group_absent(p.pid), "Git read failed or group not joined")
        (root / "active.json").unlink()
        return bytes(out)
    except BaseException:
        if p is not None:
            stop_group(p)
            if p.poll() is not None and process_group_absent(p.pid):
                (root / "active.json").unlink()
        raise
    finally:
        if sel is not None:
            sel.close()
        if p is not None:
            for stream in (p.stdin, p.stdout, p.stderr):
                if stream is not None:
                    stream.close()


def materialize(base_repo, checkout, dest, c):
    require(not dest.exists(), "source destination already exists")
    raw = git_bytes(base_repo, ["ls-tree", "-r", "-z", BASE], 1 << 20, root=dest.parent)
    entries = {}
    for entry in raw.split(b"\0"):
        if not entry:
            continue
        head, name = entry.split(b"\t", 1)
        mode, kind, oid = head.decode().split()
        name = name.decode("utf-8", "strict")
        safe_rel(name)
        require(mode in ("100644", "100755") and kind == "blob", "source special entry")
        require(name not in entries, "duplicate source path")
        entries[name] = {"mode": mode, "oid": oid}
    require(len(entries) == 1743, "base file count")
    # Read exact blob sizes first; the bounded immutable Git process is trusted to obey them.
    ids = sorted({e["oid"] for e in entries.values()})
    sizes = git_bytes(base_repo, ["cat-file", "--batch-check=%(objectname) %(objecttype) %(objectsize)"],
                      1 << 20, "".join(x + "\n" for x in ids).encode(), root=dest.parent)
    total = 0
    for line in sizes.decode().splitlines():
        oid, kind, size = line.split()
        require(oid in ids and kind == "blob" and 0 <= int(size) <= FILE_LIMIT, "base blob bound")
        total += int(size)
    require(len(sizes.splitlines()) == len(ids) and total <= SOURCE_LIMIT, "base source bound")
    blobs = git_bytes(base_repo, ["cat-file", "--batch"], SOURCE_LIMIT + (1 << 20),
                      "".join(x + "\n" for x in ids).encode(), root=dest.parent)
    table = {}
    at = 0
    for expected in ids:
        end = blobs.index(b"\n", at)
        oid, kind, size = blobs[at:end].decode().split()
        require(oid == expected and kind == "blob", "Git batch identity")
        at = end + 1
        data = blobs[at:at + int(size)]
        require(len(data) == int(size) and blobs[at + int(size):at + int(size) + 1] == b"\n",
                "Git batch framing")
        require(hashlib.sha1(b"blob " + str(len(data)).encode() + b"\0" + data).hexdigest() == oid,
                "Git blob content mismatch")
        table[oid] = data
        at += int(size) + 1
    require(at == len(blobs), "Git batch trailing data")
    output = {name: (e["mode"], table[e["oid"]]) for name, e in entries.items()}
    for row in c["overlay"]:
        name = str(safe_rel(row["path"]))
        fixture = str(safe_rel(row["fixture"]))
        require(fixture == "tests/fixtures/offline-first-compile-overlay/" + name + ".txt", "unapproved fixture path")
        target = checkout / fixture
        current = checkout
        for part in safe_rel(fixture).parts:
            current = current / part
            require(not current.is_symlink(), "overlay symlink component")
        require(target.resolve().is_relative_to(checkout.resolve()), "overlay path escaped checkout")
        got = digest_file(target)
        require(got["bytes"] == row["bytes"] and got["sha256"] == row["sha256"], "overlay mismatch")
        prior = entries.get(name)
        require((prior["oid"] if prior else None) == row["baseBlob"], "base preimage mismatch")
        data = target.read_bytes()
        data.decode("utf-8", "strict")
        output[name] = (row["mode"], data)
    require(sum(x["bytes"] for x in c["overlay"]) <= 1 << 20, "overlay aggregate limit")
    require(len(output) == 1769 and sum(len(v[1]) for v in output.values()) <= SOURCE_LIMIT, "source union bound")
    require(tree_id(output) == TREE, "prospective source tree mismatch")
    go_hash = hashlib.sha256()
    go_count = go_bytes = 0
    dest.mkdir(mode=0o700)
    for name, (mode, data) in sorted(output.items()):
        f = dest / name
        f.parent.mkdir(parents=True, exist_ok=True)
        f.write_bytes(data)
        f.chmod(0o555 if mode == "100755" else 0o444)
        if name.endswith(".go"):
            go_count += 1
            go_bytes += len(data)
            go_hash.update(name.encode() + b"\0" + data + b"\0")
    require(go_count == 1311 and go_bytes == 6716139 and go_hash.hexdigest() == c["wholeGoSHA256"],
            "whole Go identity")
    return {"base": BASE, "tree": TREE, "files": len(output), "goFiles": go_count,
            "goBytes": go_bytes, "wholeGoSHA256": go_hash.hexdigest(), "noGitObjectWrites": True}


def tree_id(files):
    tree = {}
    for path, (mode, data) in files.items():
        node = tree
        parts = safe_rel(path).parts
        for part in parts[:-1]:
            node = node.setdefault(part, {})
        node[parts[-1]] = (mode, hashlib.sha1(b"blob " + str(len(data)).encode() + b"\0" + data).digest())
    def visit(node):
        data = b""
        for name, value in sorted(node.items(), key=lambda kv: (kv[0] + ("/" if isinstance(kv[1], dict) else "")).encode()):
            mode, oid = ("40000", visit(value)) if isinstance(value, dict) else value
            data += mode.encode() + b" " + name.encode() + b"\0" + oid
        return hashlib.sha1(b"tree " + str(len(data)).encode() + b"\0" + data).digest()
    return visit(tree).hex()


def create_owner(parent, sdk, job_start):
    parent = parent.resolve(strict=True)
    require(parent.is_dir() and not parent.is_symlink(), "invalid runner temp")
    require(len(str(parent)) <= 512 and not any(ch.isspace() for ch in str(parent)), "unsafe temp transport path")
    require(sdk in SDK_VERSIONS and time.time() - 5400 < job_start <= time.time() + 5, "SDK/deadline input")
    root = parent / ("nextgo-offline-first-compile." + sdk + "." + secrets.token_hex(16))
    root.mkdir(mode=0o700)
    s = root.lstat()
    owner = {"version": 1, "uid": os.getuid(), "dev": s.st_dev, "inode": s.st_ino,
             "root": str(root), "sdk": sdk, "jobStart": job_start,
             "run": os.environ.get("GITHUB_RUN_ID", ""), "attempt": os.environ.get("GITHUB_RUN_ATTEMPT", ""),
             "nonce": secrets.token_hex(32)}
    write_json(root / "owner.json", owner)
    for name in ("home", "tmp/gobuild", "go-cache", "mod-cache", "dist", "logs", "receipts", "meta"):
        (root / name).mkdir(mode=0o700, parents=True, exist_ok=True)
    return root, owner


def owner(root):
    require(root.is_absolute() and not root.is_symlink() and root.resolve(strict=True) == root, "unsafe owned root")
    s = root.lstat()
    o = read_json(root / "owner.json", 65536)
    require(stat.S_ISDIR(s.st_mode) and s.st_uid == os.getuid() and stat.S_IMODE(s.st_mode) == 0o700
            and o["root"] == str(root) and o["uid"] == os.getuid()
            and (s.st_dev, s.st_ino) == (o["dev"], o["inode"])
            and root.name.startswith("nextgo-offline-first-compile."), "owner marker mismatch")
    return o


def inventory_sdk(sdk):
    sdk = sdk.resolve(strict=True)
    inventory = {}
    folder = sdk / "pkg/tool/linux_amd64"
    entries = sorted(folder.iterdir())
    require(len(entries) <= 32, "SDK tool count")
    total = 0
    for p in entries:
        require(p.name not in inventory and os.access(p, os.X_OK), "nonexecutable SDK tool")
        info = digest_file(p, 128 << 20)
        total += info["bytes"]
        inventory[p.name] = info
    require(total <= 512 << 20 and TOOLS <= inventory.keys(), "SDK tool closure bound")
    fixed = {}
    for name in ("VERSION", "bin/go", "bin/gofmt", "lib/wasm/go_js_wasm_exec",
                 "lib/wasm/wasm_exec_node.js", "lib/wasm/wasm_exec.js"):
        fixed[name] = digest_file(sdk / name, 128 << 20)
    return {"sdkRoot": str(sdk), "tools": inventory, "fixed": fixed,
            "archiveSHA256": None, "archiveProvenance": "UNKNOWN_UNQUALIFIED_FIRST_DEBUG_EXCEPTION",
            "kernelExecAudit": False, "kernelNetworkDeny": False}


def child_env(root, sdk, extra=None):
    env = {"PATH": str(sdk / "bin") + ":/usr/bin:/bin", "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8",
           "HOME": str(root / "home"), "TMPDIR": str(root / "tmp"), "GOTMPDIR": str(root / "tmp/gobuild"),
           "GOCACHE": str(root / "go-cache"), "GOMODCACHE": str(root / "mod-cache"),
           "GOTOOLCHAIN": "local", "GOENV": "off", "GOWORK": "off", "GOTELEMETRY": "off",
           "GOPROXY": "off", "GOSUMDB": "off", "GOMAXPROCS": "1", "GOMEMLIMIT": "384MiB",
           "GOFLAGS": "-p=1 -buildvcs=false", "CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": "amd64"}
    allowed = {"GOOS": ("linux", "js"), "GOARCH": ("amd64", "wasm"), "CGO_ENABLED": ("0",)}
    for key, value in (extra or {}).items():
        require(key in allowed and value in allowed[key],
                "unsafe environment override")
        env[key] = value
    return env


def disk_usage(root):
    total = count = 0
    categories = {"cache": 0, "tmp": 0, "dist": 0, "logs": 0, "other": 0}
    for folder, dirs, names in os.walk(root, followlinks=False):
        require(len(Path(folder).relative_to(root).parts) <= 24, "disk tree depth")
        for name in dirs + names:
            p = Path(folder) / name
            s = p.lstat()
            require(not stat.S_ISLNK(s.st_mode), "owned root symlink")
            if stat.S_ISREG(s.st_mode):
                count += 1
                total += s.st_size
                first = p.relative_to(root).parts[0]
                key = "cache" if first == "go-cache" else "tmp" if first == "tmp" else "dist" if first == "dist" else "logs" if first in ("logs", "meta", "receipts") else "other"
                categories[key] += s.st_size
                require(count <= 8192, "disk inventory count")
    require(total <= DISK_LIMIT, "monitored disk threshold")
    require(categories["cache"] <= 4 << 30 and categories["tmp"] <= 3 << 30
            and categories["dist"] <= 512 << 20 and categories["logs"] <= 256 << 20
            and categories["other"] <= 256 << 20, "monitored disk category threshold")
    return total


def process_group_absent(pgid):
    try:
        os.killpg(pgid, 0)
        return False
    except ProcessLookupError:
        return True
    except OSError:
        return False


def stop_group(p):
    errors = []
    for sig in (signal.SIGTERM, signal.SIGKILL):
        # Do not signal a numeric process-group ID after its owned leader has been reaped.
        # Surviving descendants then remain UNKNOWN; no privilege/cgroup or PID-reuse claim.
        if p.poll() is not None:
            break
        try:
            os.killpg(p.pid, sig)
        except ProcessLookupError:
            pass
        except OSError as exc:
            errors.append(type(exc).__name__)
        try:
            p.wait(timeout=2)
        except (subprocess.TimeoutExpired, OSError) as exc:
            errors.append(type(exc).__name__)
    return errors


def supervise(argv, cwd, env, root, limits, job_deadline):
    require(time.time() < job_deadline, "whole-job deadline")
    started = time.monotonic()
    pending = {"pid": None, "pgid": None, "argv": argv, "started": time.time(), "phase": "STARTING_UNKNOWN"}
    write_json(root / "active.json", pending)
    p = None
    sel = None
    outputs = {"stdout": bytearray(), "stderr": bytearray()}
    problem = None
    peak = 0
    last_sample = time.monotonic()
    max_interval = 0.0
    termination_errors = []
    try:
        p = subprocess.Popen(argv, cwd=cwd, env=env, stdin=subprocess.DEVNULL,
                             stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
        pending.update({"pid": p.pid, "pgid": p.pid, "phase": "RUNNING"})
        write_json(root / "active.json", pending)
        sel = selectors.DefaultSelector()
        for stream, name in ((p.stdout, "stdout"), (p.stderr, "stderr")):
            os.set_blocking(stream.fileno(), False)
            sel.register(stream, selectors.EVENT_READ, name)
        while sel.get_map() or p.poll() is None:
            now = time.monotonic()
            if time.time() >= job_deadline or now - started >= limits["timeoutMS"] / 1000:
                raise Refusal("command/whole-job deadline")
            if now - last_sample >= 0.10:
                # Record start-to-start intervals, including the preceding inventory walk.
                max_interval = max(max_interval, now - last_sample)
                require(now - last_sample <= 0.250, "disk sampling interval exceeded")
                last_sample = now
                peak = max(peak, disk_usage(root))
                require(time.monotonic() - last_sample <= 0.250, "disk inventory exceeded sample interval")
            for key, _ in sel.select(0.05):
                data = os.read(key.fileobj.fileno(), 65536)
                if not data:
                    sel.unregister(key.fileobj)
                    continue
                name = key.data
                bound = limits.get(name + "LimitBytes", 1 << 20)
                remaining = max(0, bound - len(outputs[name]))
                outputs[name].extend(data[:remaining])
                require(len(data) <= remaining, "command output cap; bounded prefix retained")
        p.wait(timeout=5)
    except BaseException as exc:
        problem = exc
        if p is not None:
            termination_errors = stop_group(p)
    finally:
        if sel is not None:
            sel.close()
        if p is not None:
            for stream in (p.stdout, p.stderr):
                if stream is not None:
                    stream.close()
    joined = p is not None and p.poll() is not None and process_group_absent(p.pid)
    proof = {**pending, "exitCode": p.poll() if p is not None else None, "joined": joined,
             "durationMS": int((time.monotonic() - started) * 1000),
             "diskObservedPeakBytes": peak, "diskMaximumObservedSampleIntervalMS": int(max_interval * 1000),
             "diskHardQuota": False, "stdoutBytes": len(outputs["stdout"]), "stderrBytes": len(outputs["stderr"]),
             "error": str(problem)[:256] if problem else None}
    proof["terminationErrors"] = termination_errors
    write_json(root / "last-command.json", proof)
    if joined:
        (root / "active.json").unlink()
    return proof, bytes(outputs["stdout"]), bytes(outputs["stderr"])


def artifact(path, kind):
    require(kind in ("native", "wasm", "archive"), "unknown artifact kind")
    info = digest_file(path, 128 << 20)
    with path.open("rb") as stream:
        head = stream.read(64)
    if kind == "wasm":
        require(head[:8] == b"\x00asm\x01\x00\x00\x00", "invalid WASM artifact header")
    elif kind == "archive":
        require(head[:8] == b"!<arch>\n", "invalid package archive header")
    else:
        require(head[:5] == b"\x7fELF\x02" and head[5:6] == b"\x01" and head[18:20] == b"\x3e\x00",
                "invalid native amd64 ELF header")
    return {**info, "kind": kind, "headerHex": head.hex(), "executed": False,
            "goVersionM": None, "buildInfoQualification": "NOT_EXECUTED_OR_INFERRED; compiler/link receipts separate"}


def substitute(value, root, sdk):
    return value.replace("@TASK@", str(root)).replace("@FROZEN_SOURCE@", str(root / "source")).replace("@GO@", str(sdk / "bin/go"))


def check_graph(data, root, selection=False):
    lines = data.decode("utf-8", "strict").splitlines()
    require(len(lines) <= 512, "graph package count")
    for row in lines:
        name = row.split("\t", 1)[0].split(" [", 1)[0]
        local = name in (MODULE, MODULE + ".test") or name.startswith(MODULE + "/")
        sdk = Path(read_json(root / "state.json")["sdkRoot"])
        require(name and (local or ("." not in name.split("/", 1)[0] and (sdk / "src" / name).is_dir())),
                "external/unknown module graph")
        require("runtime/cgo" not in name, "unexpected cgo graph")
        if selection:
            fields = row.split("\t")
            require(len(fields) == 5 and Path(fields[1]).resolve().is_relative_to(root / "source"), "source selection outside owned source")
    return lines


def run(root, c):
    require(hashlib.sha256(encode(c)).hexdigest() == CONTRACT_CANONICAL_SHA256, "unreviewed in-memory contract")
    o = owner(root)
    require(not (root / "summary.json").exists() and not (root / "active.json").exists(), "run replay/unknown child")
    state = read_json(root / "state.json")
    require(tuple(state["compileIDs"]) == COMPILE_IDS and tuple(state["commandIDs"]) == COMMAND_IDS,
            "unexpected stored command lists")
    sdk = Path(state["sdkRoot"])
    require(digest_file(Path(__file__).resolve(strict=True))["sha256"] == state["runnerSHA256"], "runner changed")
    original = read_json(root / "meta/toolchain.json")
    require(inventory_sdk(sdk) == original["installation"], "SDK changed before run")
    verify_source(root / "source")
    env = child_env(root, sdk)
    wrapper = root / "toolexec"
    wrapper.write_text("#!/usr/bin/python3 -I\nimport sys\nsys.dont_write_bytecode=True\nimport runpy\nsys.argv=[" + repr(state["runner"]) + ", 'tool', '--root', " + repr(str(root)) + ", '--']+sys.argv[1:]\nrunpy.run_path(" + repr(state["runner"]) + ",run_name='__main__')\n")
    wrapper.chmod(0o500)
    env["GOFLAGS"] += " -toolexec=" + str(wrapper)
    proofs, receipts, graphs, selections = [], [], {}, {}
    status = "INCOMPLETE"
    error = None
    try:
        for index, command in enumerate(c["metadataCommands"] + c["commands"]):
            key = command["id"]
            argv = [str(sdk / "bin/go")] + [substitute(x, root, sdk) for x in command["argv"]]
            extra = command.get("envOverrides", {})
            command_env = child_env(root, sdk, extra)
            command_env["GOFLAGS"] = env["GOFLAGS"]
            command_env["NEXTGO_FIRST_COMPILE_ROOT"] = str(root)
            command_env["NEXTGO_FIRST_COMPILE_COMMAND"] = key
            if command.get("artifact"):
                Path(substitute(command["artifact"], root, sdk)).parent.mkdir(parents=True, exist_ok=True)
            proof, stdout, stderr = supervise(argv, root / "source", command_env, root, command, o["jobStart"] + 5400)
            proof.update({"id": key, "ordinal": index + 1})
            if index < 13:
                # Fixed metadata records preserve failed selection/identity output too; no extra upload files.
                for name, data in (("stdout", stdout), ("stderr", stderr)):
                    proof[name] = {"prefix": data[:16384].decode("utf-8", "replace"),
                                   "sha256": hashlib.sha256(data).hexdigest(), "bytes": len(data),
                                   "prefixTruncated": len(data) > 16384}
            proofs.append(proof)
            for name, data in (("stdout", stdout), ("stderr", stderr)):
                (root / "logs" / (key + "." + name)).write_bytes(data)
            require(proof["joined"] and proof["exitCode"] == 0 and proof["error"] is None,
                    "failed/unknown command; bounded compiler output retained")
            if key == "go-version":
                require(stdout == ("go version go" + o["sdk"] + " linux/amd64\n").encode(), "actual SDK version")
            elif key == "go-environment":
                values = json.loads(stdout)
                require(values["CGO_ENABLED"] == "0" and values["GOTOOLCHAIN"] == "local" and values["GOENV"] == ""
                        and values["GOWORK"] == "" and values["GOVERSION"] == "go"+o["sdk"]
                        and values["GOOS"] == "linux" and values["GOARCH"] == "amd64"
                        and values["GOHOSTOS"] == "linux" and values["GOHOSTARCH"] == "amd64"
                        and values["GOFLAGS"] == command_env["GOFLAGS"]
                        and values["GOROOT"] == str(sdk) and values["GOCACHE"] == str(root / "go-cache")
                        and values["GOMODCACHE"] == str(root / "mod-cache")
                        and values["GOTMPDIR"] == str(root / "tmp/gobuild"), "actual Go environment")
                write_json(root / "meta/environment.json", values)
            elif key.startswith("graph-"):
                graphs[key] = check_graph(stdout, root)
            elif key.startswith("source-selection-"):
                selections[key] = check_graph(stdout, root, True)
            else:
                path = Path(substitute(command["artifact"], root, sdk))
                kind = "wasm" if path.suffix == ".wasm" else "archive" if path.suffix == ".a" else "native"
                command_tools = [x for x in tool_receipts(root) if x["command"] == key]
                receipts.append({"id": key, "artifact": str(path.relative_to(root)), **artifact(path, kind),
                                 "observedTools": sorted({x["tool"] for x in command_tools if x["purpose"] == "tool-operation"}),
                                 "toolReceiptCount": len(command_tools), "freshPrivateCacheMayBeReused": True})
            require(inventory_sdk(sdk) == original["installation"], "SDK changed during run")
            verify_source(root / "source")
            disk_usage(root)
        require(len(proofs) == 38 and len(receipts) == 25, "incomplete command/artifact evidence")
        tools = tool_receipts(root)
        require(tools and any(x["tool"] == "compile" and x["purpose"] == "tool-operation" for x in tools)
                and any(x["tool"] == "link" and x["purpose"] == "tool-operation" for x in tools),
                "missing actual compiler/linker operation receipt")
        require(not any((root / "mod-cache").iterdir()), "unexpected module download/cache")
        status = "DEFAULT_SOURCE_COMPILE_LINK_ONLY_INSTALLED_SDK_DEBUG"
    except BaseException as exc:
        error = str(exc)[:512]
    finally:
        write_json(root / "meta/graphs.json", graphs)
        write_json(root / "meta/source-selection.json", selections)
        write_json(root / "meta/command-proofs.json", proofs)
        try:
            partial_tools = tool_receipts(root, allow_partial=True)
            tool_error = None
        except (OSError, ValueError, Refusal, KeyError, TypeError) as exc:
            partial_tools = []
            tool_error = type(exc).__name__
            status = "INCOMPLETE"
        write_json(root / "meta/artifact-or-execution-receipts.json", {"artifacts": receipts, "tools": partial_tools,
                   "toolReceiptError": tool_error})
        report = {"status": status, "error": error, "sdk": o["sdk"], "completedCommands": len(proofs),
                  "artifactCount": len(receipts), "archiveAuthenticityQualified": False, "testsExecuted": 0,
                  "GateBImplemented": False, "runtimeQualified": False, "sourceReady": False,
                  "nativeQualified": False, "ready": False, "childOutcomeUnknown": (root / "active.json").exists()}
        write_json(root / "summary.json", report)
        write_json(root / "meta/summary.json", report)
    require(status != "INCOMPLETE", "compile debug incomplete: " + str(error))


def tool_receipts(root, allow_partial=False):
    files = sorted((root / "receipts").iterdir())
    require(len(files) <= RECEIPT_LIMIT * 2, "tool receipt file cap")
    state = read_json(root / "state.json")
    require(tuple(state["commandIDs"]) == COMMAND_IDS, "unapproved command ownership list")
    sdk = Path(state["sdkRoot"])
    expected = read_json(root / "meta/toolchain.json")["installation"]["tools"]
    result = []
    for path in files:
        try:
            require(path.name.endswith(".json") and len(path.stem) == 4 and path.stem.isdigit()
                    and int(path.stem) < RECEIPT_LIMIT, "unfinished/noncanonical tool receipt")
            row = read_json(path, 4096)
            require(set(row) == {"tool", "path", "command", "purpose", "argvSHA256", "argvCount", "argvPreview", "argvPreviewTruncated",
                                 "before", "returned", "exitCode", "unchanged", "importPath"}, "tool receipt schema")
            require(row["tool"] in TOOLS and row["path"] == str(sdk / "pkg/tool/linux_amd64" / row["tool"])
                    and row["before"] == expected[row["tool"]], "tool receipt native identity")
            require(isinstance(row["argvSHA256"], str) and len(row["argvSHA256"]) == 64
                    and all(ch in "0123456789abcdef" for ch in row["argvSHA256"])
                    and type(row["argvCount"]) is int and 0 <= row["argvCount"] <= 8192
                    and type(row["returned"]) is bool and type(row["unchanged"]) is bool
                    and row["command"] in state["commandIDs"] and row["purpose"] in ("version-query", "tool-operation"),
                    "tool receipt fields")
            require(isinstance(row["argvPreview"], list) and len(row["argvPreview"]) <= 16
                    and all(isinstance(x, str) and len(x) <= 96 for x in row["argvPreview"])
                    and type(row["argvPreviewTruncated"]) is bool
                    and isinstance(row["importPath"], str) and len(row["importPath"]) <= 512,
                    "tool receipt preview bounds")
            if not allow_partial:
                require(row["returned"] and type(row["exitCode"]) is int and row["exitCode"] == 0
                        and row["unchanged"], "unknown/failed native SDK tool")
            result.append(row)
        except (OSError, ValueError, Refusal, KeyError, TypeError) as exc:
            require(allow_partial, "malformed/partial tool receipt")
            result.append({"file": path.name[:64], "returned": False, "exitCode": None,
                           "unchanged": False, "error": type(exc).__name__})
    require(allow_partial or len(result) <= RECEIPT_LIMIT, "tool invocation count")
    return result


def tool(root, args):
    o = owner(root)
    require(args and time.time() < o["jobStart"] + 5400, "tool deadline/arguments")
    state = read_json(root / "state.json")
    sdk = Path(state["sdkRoot"])
    path = Path(args[0])
    require(path.is_absolute() and path.parent == sdk / "pkg/tool/linux_amd64" and path.name in TOOLS
            and path.resolve(strict=True) == path, "unexpected Go tool")
    expected = read_json(root / "meta/toolchain.json")["installation"]["tools"][path.name]
    before = digest_file(path, 128 << 20)
    require(before == expected, "SDK tool identity changed")
    # -p=1 is the admitted producer setting; exclusive numbered reservation refuses overflow/replay.
    receipt_dir = root / "receipts"
    slot = None
    for n in range(RECEIPT_LIMIT):
        candidate = receipt_dir / (str(n).zfill(4) + ".json")
        try:
            fd = os.open(candidate, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
            slot = candidate
            break
        except FileExistsError:
            continue
    require(slot is not None, "SDK invocation receipt count")
    require(len(args) <= 8193 and len(encode(args[1:])) <= 512 << 10, "native tool argv bound")
    row = {"tool": path.name, "path": str(path), "before": before,
           "command": os.environ.get("NEXTGO_FIRST_COMPILE_COMMAND"),
           "purpose": "version-query" if args[1:] == ["-V=full"] else "tool-operation",
           "argvSHA256": hashlib.sha256(encode(args[1:])).hexdigest(), "argvCount": len(args) - 1,
           "argvPreview": [x[:96] for x in args[1:17]], "argvPreviewTruncated": len(args) > 17 or any(len(x) > 96 for x in args[1:17]),
           "returned": False, "exitCode": None, "unchanged": False,
           "importPath": os.environ.get("TOOLEXEC_IMPORTPATH", "")[:512]}
    require(len(encode(row)) <= 4096, "tool argument receipt cap")
    require(row["command"] in state["commandIDs"], "unknown owning Go command")
    with os.fdopen(fd, "wb") as f:
        f.write(encode(row))
    # Transparent inherited streams and exact argv: no output parsing, rewriting, shell, or retry.
    p = subprocess.Popen(args, env=os.environ.copy())
    code = p.wait()
    row.update({"returned": True, "exitCode": code, "unchanged": digest_file(path, 128 << 20) == before})
    write_json(slot, row, 4096)
    if code < 0:
        signal.signal(-code, signal.SIG_DFL)
        os.kill(os.getpid(), -code)
    return code


def collect(root):
    o = owner(root)
    state = read_json(root / "state.json")
    require(tuple(state["compileIDs"]) == COMPILE_IDS, "unapproved collector log allowlist")
    evidence = Path(state["evidence"])
    es = evidence.lstat()
    require(evidence.parent == root.parent and evidence.name.startswith("nextgo-first-compile-evidence.")
            and stat.S_ISDIR(es.st_mode) and es.st_uid == os.getuid() and stat.S_IMODE(es.st_mode) == 0o700
            and (es.st_dev, es.st_ino) == (state["evidenceDev"], state["evidenceInode"]), "unowned evidence stage")
    require(not (evidence / "collection.json").exists(), "collection replay")
    require(not any(evidence.iterdir()), "nonempty evidence stage")
    chosen = []
    for name in METADATA:
        chosen.append((root / "meta" / name, name, False))
    for n in state["compileIDs"]:
        for channel in ("stdout", "stderr"):
            chosen.append((root / "logs" / (n + "." + channel), n + "." + channel + ".gz", True))
    require(len(chosen) == 58, "collector allowlist count")
    rows, total = [], 0
    for path, name, compressed in chosen:
        try:
            regular(path, 4 << 20)
            raw = path.read_bytes()
            data = gzip.compress(raw, mtime=0) if compressed else raw
            require(len(data) <= UPLOAD_FILE_LIMIT and total + len(data) <= COLLECT_LIMIT - 65536, "collection cap")
            dest = evidence / name
            fd = os.open(dest, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
            with os.fdopen(fd, "wb") as out:
                out.write(data)
            total += len(data)
            rows.append({"name": name, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest(),
                         "originalBytes": len(raw), "originalSHA256": hashlib.sha256(raw).hexdigest()})
        except (OSError, Refusal) as exc:
            rows.append({"name": name, "omitted": str(exc)[:160]})
    result = {"files": rows, "bytes": total, "complete": all("omitted" not in x for x in rows),
              "sdk": o["sdk"], "sourceReady": False, "runtimeQualified": False}
    write_json(evidence / "collection.json", result, 65536)
    return result


def cleanup(root, uploaded):
    owner(root)
    state = read_json(root / "state.json")
    require(uploaded and not (root / "active.json").exists(), "upload absent or child not joined; retain root")
    evidence = Path(state["evidence"])
    es = evidence.lstat()
    require(evidence.parent == root.parent and stat.S_ISDIR(es.st_mode)
            and (es.st_dev, es.st_ino) == (state["evidenceDev"], state["evidenceInode"]),
            "evidence custody changed")
    result = read_json(evidence / "collection.json", 65536)
    require(result["complete"], "incomplete evidence; retain root")
    require(len(result["files"]) == 58, "collection shape")
    for row in result["files"]:
        name = str(safe_rel(row["name"]))
        require(len(PurePosixPath(name).parts) == 1, "collection path")
        got = digest_file(evidence / name, UPLOAD_FILE_LIMIT)
        require(got["bytes"] == row["bytes"] and got["sha256"] == row["sha256"], "collected evidence changed")
    # Reject symlinks anywhere before deleting; exact root inode/marker checked again immediately before rmtree.
    disk_usage(root)
    owner(root)
    shutil.rmtree(root)


def init(args, c):
    require(sys.platform == "linux" and os.getuid() >= 0, "hosted Linux required")
    checkout, base_repo = Path(args.checkout).resolve(strict=True), Path(args.base_repo).resolve(strict=True)
    parent = Path(os.environ["RUNNER_TEMP"]).resolve(strict=True)
    require(not parent.is_relative_to(checkout) and not parent.is_relative_to(base_repo), "temp beneath source")
    root, o = create_owner(parent, args.sdk, float(args.job_start))
    evidence = parent / ("nextgo-first-compile-evidence." + args.sdk + "." + secrets.token_hex(16))
    evidence.mkdir(mode=0o700)
    es = evidence.lstat()
    state = {"sdkRoot": None, "evidence": str(evidence), "evidenceDev": es.st_dev,
             "evidenceInode": es.st_ino, "runner": str(Path(__file__).resolve(strict=True)),
             "runnerSHA256": digest_file(Path(__file__).resolve(strict=True))["sha256"],
             "compileIDs": [x["id"] for x in c["commands"]],
             "commandIDs": [x["id"] for x in c["metadataCommands"] + c["commands"]]}
    write_json(root / "state.json", state)
    env_path = os.environ.get("GITHUB_ENV")
    require(env_path is not None, "missing hosted environment file")
    # Publish custody before SDK/source/Git work, so initialization failure can be collected honestly.
    with open(env_path, "a", encoding="utf-8") as f:
        f.write("NEXTGO_FIRST_COMPILE_ROOT=" + str(root) + "\nNEXTGO_FIRST_COMPILE_EVIDENCE=" + str(evidence) + "\n")
    try:
        go = Path(shutil.which("go") or "").resolve(strict=True)
        require(go.name == "go" and go.parent.name == "bin", "SDK executable location")
        sdk = go.parent.parent
        state["sdkRoot"] = str(sdk)
        write_json(root / "state.json", state)
        installation = inventory_sdk(sdk)
        provisioning = read_json(Path(os.environ["NEXTGO_FIRST_COMPILE_PROVISIONING_JSON"]), 65536)
        require(provisioning["version"] == 1, "unknown provisioning record")
        write_json(root / "meta/toolchain.json", {"installation": installation,
                   "python": digest_file(Path(sys.executable).resolve(strict=True), 128 << 20),
                   "git": digest_file(Path("/usr/bin/git"), 128 << 20), "provisioning": provisioning,
                   "setupGoOutputVersion": os.environ.get("NEXTGO_FIRST_COMPILE_SETUP_GO_VERSION"),
                   "actionAndContainerAuthenticityQualified": False})
        source = materialize(base_repo, checkout, root / "source", c)
        write_json(root / "meta/source-identity.json", source)
        disk_usage(root)
    except BaseException as exc:
        report = {"status": "INITIALIZATION_INCOMPLETE", "error": str(exc)[:512],
                  "sdk": args.sdk, "childOutcomeUnknown": (root / "active.json").exists(),
                  "completedCommands": 0, "testsExecuted": 0, "sourceReady": False,
                  "runtimeQualified": False, "ready": False, "archiveAuthenticityQualified": False}
        write_json(root / "summary.json", report)
        write_json(root / "meta/summary.json", report)
        raise
    return root


def main():
    def interrupted(signum, frame):
        raise Refusal("supervisor interrupted by signal " + str(signum))
    signal.signal(signal.SIGTERM, interrupted)
    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=("init", "run", "collect", "cleanup", "tool"))
    parser.add_argument("--contract")
    parser.add_argument("--root", default=os.environ.get("NEXTGO_FIRST_COMPILE_ROOT"))
    parser.add_argument("--sdk", choices=SDK_VERSIONS)
    parser.add_argument("--job-start")
    parser.add_argument("--checkout")
    parser.add_argument("--base-repo")
    parser.add_argument("--uploaded", action="store_true")
    args, extra = parser.parse_known_args()
    if args.mode == "tool":
        return tool(Path(args.root), extra[1:] if extra[:1] == ["--"] else extra)
    require(not extra, "unexpected arguments")
    c = contract(Path(args.contract))
    if args.mode == "init":
        init(args, c)
    elif args.mode == "run":
        run(Path(args.root), c)
    elif args.mode == "collect":
        collect(Path(args.root))
    else:
        cleanup(Path(args.root), args.uploaded)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (Refusal, OSError, ValueError, KeyError) as exc:
        print("GateA refusal: " + str(exc)[:512], file=sys.stderr)
        sys.exit(1)
