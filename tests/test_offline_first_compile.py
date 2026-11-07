"""PRIVATE SOURCE ONLY: authored, never imported/parsed/executed by the author.

Stdlib protocol tests. Every process/signal boundary is denied by default; tests
replace it with in-memory doubles. SDK-looking files below are synthetic bytes.
No Go, Node, network, compiler, candidate artifact or installed SDK is executed.
The Mother runner and exact contract remain separately reviewed inputs.
"""
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import tempfile
import time
import types
import unittest
from unittest import mock

HERE = Path(__file__).resolve().parent
RUNNER = HERE / "offline-first-compile.py"
if not RUNNER.is_file():
    RUNNER = HERE.parent / "source/tests/offline-first-compile.py"
CONTRACT = RUNNER.with_name("offline-first-compile-source.json")


# Exact untruncated stdout from run40979960218/job123817277284 command2.
# Saved data only; historical paths below are never opened or executed.
ACTUAL_ENVIRONMENT_STDOUT = b'{\n\t"CC": "gcc",\n\t"CGO_ENABLED": "0",\n\t"GOARCH": "amd64",\n\t"GOCACHE": "/__w/_temp/nextgo-offline-first-compile.1.24.13.58e9eeb2425554c4baf842f63b2bd2cd/go-cache",\n\t"GOENV": "",\n\t"GOFLAGS": "-p=1 -buildvcs=false -toolexec=/__w/_temp/nextgo-offline-first-compile.1.24.13.58e9eeb2425554c4baf842f63b2bd2cd/toolexec",\n\t"GOHOSTARCH": "amd64",\n\t"GOHOSTOS": "linux",\n\t"GOMODCACHE": "/__w/_temp/nextgo-offline-first-compile.1.24.13.58e9eeb2425554c4baf842f63b2bd2cd/mod-cache",\n\t"GOOS": "linux",\n\t"GOROOT": "/__t/go/1.24.13/x64",\n\t"GOTMPDIR": "/__w/_temp/nextgo-offline-first-compile.1.24.13.58e9eeb2425554c4baf842f63b2bd2cd/tmp/gobuild",\n\t"GOTOOLCHAIN": "local",\n\t"GOVERSION": "go1.24.13",\n\t"GOWORK": "off"\n}\n'
ACTUAL_ENVIRONMENT_STDOUT_SHA256 = "348fc32dfffdfd14459ab33f7f86281c037f74153ee0aad0ccb137c597cc6d99"


# Saved scalar projection only. The actual walk-versus-scheduler cause split is UNKNOWN.
# The separate209ms+50ms virtual scenario below is a regression construction, not an incident diagnosis.
ACTUAL_DISK_CADENCE_WITNESS = {'id': 'graph-native', 'ordinal': 3, 'joined': True, 'exitCode': -15, 'error': 'disk sampling interval exceeded', 'diskMaximumObservedSampleIntervalMS': 259, 'diskObservedPeakBytes': 11932091, 'durationMS': 6901, 'stdoutBytes': 0, 'stderrBytes': 0, 'run': 40985777074, 'job': 123836329595, 'sourceProofsSHA256': '6aadda8d8fd5be8eca3c4f55b06bc1147cf516b22c7023e670d0b23e7b494b8e', 'actualCauseSplit': 'UNKNOWN'}


def load_runner():
    spec = importlib.util.spec_from_file_location("private_gatea_runner_under_test", RUNNER)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def elf():
    value = bytearray(64)
    value[:6] = b"\x7fELF\x02\x01"
    value[18:20] = b"\x3e\x00"
    return bytes(value)


def blob(data):
    return hashlib.sha1(b"blob " + str(len(data)).encode() + b"\0" + data).hexdigest()


class FakeStream:
    def __init__(self, fd):
        self.fd = fd
        self.closed = False
    def fileno(self):
        return self.fd
    def close(self):
        self.closed = True


class FakeChild:
    """No real child: wait/poll/join and streams are supplied protocol facts."""
    def __init__(self, code=0, join=True):
        self.pid = 424242
        self.returncode = code
        self.join = join
        self.stdout, self.stderr = FakeStream(71), FakeStream(72)
        self.waits = []
    def poll(self):
        return self.returncode
    def wait(self, timeout=None):
        self.waits.append(timeout)
        if not self.join:
            raise subprocess.TimeoutExpired(["synthetic-child"], timeout)
        if self.returncode is None:
            self.returncode = -signal.SIGKILL
        return self.returncode


class FakeSelector:
    def __init__(self, interrupted=False):
        self.keys = {}
        self.interrupted = interrupted
        self.closed = False
    def register(self, stream, events, data):
        self.keys[stream.fileno()] = types.SimpleNamespace(fileobj=stream, data=data)
    def unregister(self, stream):
        del self.keys[stream.fileno()]
    def get_map(self):
        return self.keys
    def select(self, timeout):
        if self.interrupted:
            raise KeyboardInterrupt("synthetic cancellation")
        return [(value, 1) for value in list(self.keys.values())]
    def close(self):
        self.closed = True


class CadenceClock:
    """Virtual seconds; reads never advance time, waits/walk fixtures do."""
    def __init__(self):
        self.now = 0.0
    def monotonic(self):
        return self.now
    def wall(self):
        return 1000.0 + self.now
    def advance(self, seconds):
        self.now += seconds


class CadenceSelector(FakeSelector):
    def __init__(self, clock, scans, delayed_first_wait=0.0):
        super().__init__()
        self.clock, self.scans = clock, scans
        self.delayed_first_wait = delayed_first_wait
        self.waits = []
    def select(self, timeout):
        # A hard fixture bound prevents a broken supervisor from spinning forever.
        if len(self.waits) >= 16:
            raise AssertionError("virtual selector iteration bound")
        before = self.clock.now
        self.waits.append({"at": before, "timeout": timeout, "scans": len(self.scans)})
        self.clock.advance(timeout + (self.delayed_first_wait if len(self.waits) == 1 else 0.0))
        # Keep both synthetic pipes open until two monitored scans have been observed.
        return [(value, 1) for value in list(self.keys.values())] if len(self.scans) >= 2 else []


class GateARunnerTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.r = load_runner()

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="gatea-unit-only.")
        self.addCleanup(self.temp.cleanup)
        self.parent = Path(self.temp.name).resolve()
        self.denied = []
        for name in ("Popen",):
            patch = mock.patch.object(self.r.subprocess, name,
                side_effect=AssertionError("unmocked subprocess forbidden"))
            self.denied.append(patch.start())
            self.addCleanup(patch.stop)
        for name in ("kill", "killpg"):
            patch = mock.patch.object(self.r.os, name,
                side_effect=AssertionError("unmocked signal forbidden"))
            patch.start()
            self.addCleanup(patch.stop)
        self.root, self.marker = self.r.create_owner(self.parent, self.r.SDK_VERSIONS[0], time.time())
        self.sdk = self.parent / "synthetic-sdk"
        for name in self.r.TOOLS:
            self.put(self.sdk / "pkg/tool/linux_amd64" / name, elf(), 0o700)
        for name in ("VERSION", "bin/go", "bin/gofmt", "lib/wasm/go_js_wasm_exec",
                     "lib/wasm/wasm_exec_node.js", "lib/wasm/wasm_exec.js"):
            self.put(self.sdk / name, b"SYNTHETIC SDK FIXTURE ONLY\n", 0o700)
        self.installation = self.r.inventory_sdk(self.sdk)
        self.evidence = self.parent / "nextgo-first-compile-evidence.unit"
        self.evidence.mkdir(mode=0o700)
        self.r.write_json(self.root / "state.json", {"sdkRoot": str(self.sdk),
            "evidence": str(self.evidence), "runner": str(RUNNER), "compileIDs": list(self.r.COMPILE_IDS), "commandIDs": list(self.r.COMMAND_IDS),
            "runnerSHA256": self.r.digest_file(RUNNER)["sha256"],
            "evidenceDev": self.evidence.stat().st_dev, "evidenceInode": self.evidence.stat().st_ino})
        self.r.write_json(self.root / "meta/toolchain.json", {"installation": self.installation})
        (self.root / "source").mkdir()

    def put(self, path, data, mode=0o600):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
        path.chmod(mode)
        return path

    def plan(self):
        # Exact actual contract is an input, not generated expected command fields.
        return copy.deepcopy(self.r.contract(CONTRACT))

    def state(self, ids, command_ids=None):
        value = self.r.read_json(self.root / "state.json")
        value["compileIDs"] = list(ids)
        value["commandIDs"] = list(command_ids if command_ids is not None else self.r.COMMAND_IDS)
        self.r.write_json(self.root / "state.json", value)

    def sdk_tool(self, name="compile", tail=None, command=None):
        command = command or self.r.COMPILE_IDS[0]
        args = [str(self.sdk / "pkg/tool/linux_amd64" / name)] + list(tail or [])
        child = FakeChild()
        with mock.patch.object(self.r.subprocess, "Popen", return_value=child) as launch, \
             mock.patch.dict(self.r.os.environ, {"NEXTGO_FIRST_COMPILE_COMMAND": command}):
            code = self.r.tool(self.root, args)
        return args, launch, code

    def fake_run(self, fail_at=None, fail_message="synthetic failure", joined=True,
                 mutate_receipts=None):
        c = self.plan()
        commands = c["metadataCommands"] + c["commands"]
        self.state((row["id"] for row in c["commands"]), (row["id"] for row in commands))
        calls = []
        tool_launches = []
        def fake_supervise(argv, cwd, env, root, limits, deadline):
            index = len(calls) + 1
            command = commands[index - 1]
            calls.append((list(argv), cwd, dict(env), limits["id"]))
            if fail_at == index:
                if not joined:
                    self.r.write_json(root / "active.json", {"pid": 424242, "joined": False})
                return {"exitCode": 1 if joined else None, "joined": joined, "error": fail_message, "argv": list(argv)}, b"synthetic bounded diagnostic", b""
            key = command["id"]
            stdout = b""
            if key == "go-version":
                stdout = ("go version go" + self.marker["sdk"] + " linux/amd64\n").encode()
            elif key == "go-environment":
                stdout = self.r.encode({"CGO_ENABLED": "0", "GOTOOLCHAIN": "local", "GOENV": "",
                    "GOWORK": "off", "GOVERSION": "go" + self.marker["sdk"], "GOROOT": str(self.sdk),
                    "GOCACHE": str(root / "go-cache"), "GOMODCACHE": str(root / "mod-cache"),
                    "GOOS": "linux", "GOARCH": "amd64", "GOHOSTOS": "linux", "GOHOSTARCH": "amd64",
                    "GOFLAGS": env["GOFLAGS"], "GOTMPDIR": str(root / "tmp/gobuild")})
            elif key.startswith("graph-"):
                stdout = (self.r.MODULE + "\n").encode()
            elif key.startswith("source-selection-"):
                stdout = (self.r.MODULE + "\t" + str(root / "source") + "\tfile.go\t\ttest.go\n").encode()
            else:
                path = Path(self.r.substitute(command["artifact"], root, self.sdk))
                data = b"\x00asm\x01\x00\x00\x00" if path.suffix == ".wasm" else b"!<arch>\n" if path.suffix == ".a" else elf()
                self.put(path, data)
                if not tool_launches:
                    # Actual wrapper writes its own receipts from two fake tool calls.
                    for tool_name in ("compile", "link"):
                        args, launch, code = self.sdk_tool(tool_name, ["literal-argument"], command=key)
                        tool_launches.append((args, launch.call_args, code))
            if mutate_receipts and index == len(commands):
                mutate_receipts()
            return {"exitCode": 0, "joined": True, "error": None, "argv": list(argv)}, stdout, b""
        # Source integrity itself is tested against full synthetic materialization.
        # Here its boundary is mocked to isolate command/report control flow.
        with mock.patch.object(self.r, "supervise", side_effect=fake_supervise), \
             mock.patch.object(self.r, "verify_source", return_value=True):
            try:
                self.r.run(self.root, c)
            except self.r.Refusal as error:
                failure = error
            else:
                failure = None
        return c, calls, tool_launches, failure, self.r.read_json(self.root / "summary.json")

    def actual_environment_run(self, changes=None):
        # Exercise the actual run guard with the saved report, not a mirrored predicate.
        # Each scenario gets fresh owner/state/report paths; no replay is admitted.
        root, marker = self.r.create_owner(self.parent, self.r.SDK_VERSIONS[0], time.time())
        c = self.plan()
        self.r.write_json(root / "state.json", {"sdkRoot": str(self.sdk),
            "evidence": str(self.evidence), "runner": str(RUNNER),
            "compileIDs": list(self.r.COMPILE_IDS), "commandIDs": list(self.r.COMMAND_IDS),
            "runnerSHA256": self.r.digest_file(RUNNER)["sha256"],
            "evidenceDev": self.evidence.stat().st_dev, "evidenceInode": self.evidence.stat().st_ino})
        self.r.write_json(root / "meta/toolchain.json", {"installation": self.installation})
        (root / "source").mkdir()
        values = json.loads(ACTUAL_ENVIRONMENT_STDOUT)
        calls = []
        submitted = []
        def fake_supervise(argv, cwd, env, owned_root, limits, deadline):
            key = limits["id"]
            calls.append(key)
            self.assertEqual(owned_root, root)
            self.assertEqual(cwd, root / "source")
            proof = {"exitCode": 0, "joined": True, "error": None, "argv": list(argv)}
            if key == "go-version":
                return proof, ("go version go" + marker["sdk"] + " linux/amd64\n").encode(), b""
            if key == "go-environment":
                # Rebind only these five location-dependent saved fields to the owned model.
                values.update({"GOROOT": str(self.sdk), "GOCACHE": str(root / "go-cache"),
                    "GOMODCACHE": str(root / "mod-cache"), "GOTMPDIR": str(root / "tmp/gobuild"),
                    "GOFLAGS": env["GOFLAGS"]})
                values.update(changes or {})
                data = self.r.encode(values)
                submitted.append(data)
                return proof, data, b""
            self.assertEqual(key, "graph-native")
            # Positive case must reach the next command, then stop before graph/compile work.
            return {"exitCode": 1, "joined": True, "error": "stop after environment guard",
                    "argv": list(argv)}, b"", b""
        with mock.patch.object(self.r, "supervise", side_effect=fake_supervise), \
             mock.patch.object(self.r, "verify_source", return_value=True):
            with self.assertRaises(self.r.Refusal):
                self.r.run(root, c)
        self.denied[0].assert_not_called()
        report = self.r.read_json(root / "summary.json")
        self.assertEqual(report["status"], "INCOMPLETE")
        self.assertEqual((report["artifactCount"], report["testsExecuted"]), (0, 0))
        self.assertFalse(report["ready"] or report["sourceReady"] or report["runtimeQualified"] or report["nativeQualified"])
        return root, calls, submitted[0], report

    def synthetic_source(self):
        base = {"pkg/g%04d.go" % i: ("100644", b"package fixture\n") for i in range(1287)}
        base.update({"docs/f%04d.txt" % i: ("100644", b"fixture\n") for i in range(456)})
        overlay = []
        checkout = self.parent / "checkout"
        checkout.mkdir()
        names = ["pkg/g%04d.go" % i for i in range(11)] + ["client/new%02d.go" % i for i in range(24)] + ["tests/probe.mjs", "docs/qualification.md"]
        output = dict(base)
        for name in names:
            data = b"package overlay\n" if name.endswith(".go") else b"overlay\n"
            output[name] = ("100644", data)
        current = sum(len(data) for name, (_, data) in output.items() if name.endswith(".go"))
        pad_name = "pkg/g1200.go"
        mode, value = base[pad_name]
        base[pad_name] = (mode, value + b"x" * (6716139 - current))
        output[pad_name] = base[pad_name]
        for name in names:
            mode, data = output[name]
            fixture = "tests/fixtures/offline-first-compile-overlay/" + name + ".txt"
            self.put(checkout / fixture, data)
            overlay.append({"path": name, "fixture": fixture, "mode": mode, "bytes": len(data),
                "sha256": hashlib.sha256(data).hexdigest(),
                "baseBlob": blob(base[name][1]) if name in base else None})
        rows = sorted((name, mode, blob(data)) for name, (mode, data) in base.items())
        blobs = {blob(data): data for _, data in base.values()}
        def fake_git(repo, args, cap, input_data=None, root=None):
            if args[0] == "ls-tree":
                return b"".join((mode + " blob " + oid + "\t" + name).encode() + b"\0" for name, mode, oid in rows)
            ids = input_data.decode().splitlines()
            if args[1].startswith("--batch-check"):
                return b"".join((oid + " blob " + str(len(blobs[oid])) + "\n").encode() for oid in ids)
            return b"".join((oid + " blob " + str(len(blobs[oid])) + "\n").encode() + blobs[oid] + b"\n" for oid in ids)
        h = hashlib.sha256()
        for name, (_, data) in sorted(output.items()):
            if name.endswith(".go"):
                h.update(name.encode() + b"\0" + data + b"\0")
        return checkout, {"overlay": overlay, "wholeGoSHA256": h.hexdigest()}, output, fake_git

    def materialize_fixture(self, mutate=None):
        checkout, c, output, fake_git = self.synthetic_source()
        if mutate:
            mutate(checkout, c)
        dest = self.root / "materialized"
        with mock.patch.object(self.r, "git_bytes", side_effect=fake_git), mock.patch.object(self.r, "TREE", self.r.tree_id(output)):
            result = self.r.materialize(self.parent / "unused-base", checkout, dest, c)
        return checkout, c, output, dest, result

    def supervisor(self, mode, joined):
        child = FakeChild(code=0 if mode == "normal" else None, join=joined)
        selector = FakeSelector(interrupted=mode == "cancel")
        chunks = {71: [b"oversized" if mode == "output" else b"ok", b""], 72: [b""]}
        now = iter(range(1000))
        def monotonic():
            return next(now) * (0.11 if mode == "disk" else 0.001)
        limits = {"timeoutMS": 1 if mode == "deadline" else 100000,
                  "stdoutLimitBytes": 2 if mode == "output" else 1024, "stderrLimitBytes": 1024}
        disk = mock.Mock(side_effect=self.r.Refusal("monitored disk threshold")) if mode == "disk" else mock.Mock(return_value=0)
        with mock.patch.object(self.r.subprocess, "Popen", return_value=child) as launch, \
             mock.patch.object(self.r.selectors, "DefaultSelector", return_value=selector), \
             mock.patch.object(self.r.os, "set_blocking"), \
             mock.patch.object(self.r.os, "read", side_effect=lambda fd, cap: chunks[fd].pop(0)), \
             mock.patch.object(self.r.os, "killpg") as killpg, \
             mock.patch.object(self.r, "process_group_absent", return_value=joined), \
             mock.patch.object(self.r, "disk_usage", disk), \
             mock.patch.object(self.r.time, "monotonic", side_effect=monotonic):
            try:
                self.r.supervise(["synthetic-go"], self.root, {}, self.root, limits, time.time()+100)
            except self.r.Refusal as error:
                failure = error
            else:
                failure = None
        proof = self.r.read_json(self.root / "last-command.json")
        if proof.get("error") is not None:
            failure = self.r.Refusal(proof["error"])
        return child, launch, killpg, failure, proof

    def cadence_supervisor(self, walk_seconds, delayed_first_wait=0.0,
                           command_timeout_ms=10000, whole_timeout_ms=10000):
        clock = CadenceClock()
        scans = []
        selector = CadenceSelector(clock, scans, delayed_first_wait)
        child = FakeChild(code=0)
        def inventory(owned_root):
            self.assertEqual(owned_root, self.root)
            scans.append(clock.now)
            self.assertLessEqual(len(scans), 8)
            clock.advance(walk_seconds[len(scans)-1] if len(scans) <= len(walk_seconds) else 0.0)
            return 11932091  # Synthetic returned byte counter; no filesystem walk.
        limits = {"timeoutMS": command_timeout_ms, "stdoutLimitBytes": 1024, "stderrLimitBytes": 1024}
        with mock.patch.object(self.r.subprocess, "Popen", return_value=child) as launch, \
             mock.patch.object(self.r.selectors, "DefaultSelector", return_value=selector), \
             mock.patch.object(self.r.os, "set_blocking"), \
             mock.patch.object(self.r.os, "read", return_value=b"") as read_pipe, \
             mock.patch.object(self.r, "process_group_absent", return_value=True), \
             mock.patch.object(self.r, "disk_usage", side_effect=inventory), \
             mock.patch.object(self.r.time, "monotonic", side_effect=clock.monotonic), \
             mock.patch.object(self.r.time, "time", side_effect=clock.wall):
            returned, stdout, stderr = self.r.supervise(["synthetic-cadence-child"], self.root, {},
                self.root, limits, clock.wall() + whole_timeout_ms / 1000.0)
        self.assertEqual(launch.call_count, 1)
        self.assertEqual(launch.call_args.args[0], ["synthetic-cadence-child"])
        self.assertTrue(returned["joined"])
        self.assertFalse((self.root / "active.json").exists())
        self.assertEqual((stdout, stderr), (b"", b""))
        self.assertTrue(selector.closed and child.stdout.closed and child.stderr.closed)
        self.assertLessEqual(len(selector.waits), 16)
        self.assertTrue(all(0.0 <= row["timeout"] <= 0.05 for row in selector.waits))
        self.assertEqual(returned, self.r.read_json(self.root / "last-command.json"))
        return returned, scans, selector.waits, read_pipe.call_count

    def collector_fixture(self):
        self.state(self.r.COMPILE_IDS)
        for name in self.r.METADATA:
            self.put(self.root / "meta" / name, b"{}\n")
        for command_id in self.r.COMPILE_IDS:
            for channel in ("stdout", "stderr"):
                self.put(self.root / "logs" / (command_id + "." + channel), b"bounded fixture output\n")

    def test_materialize_exact_base_and_37_postimages(self):
        _, c, output, dest, result = self.materialize_fixture()
        self.assertEqual(len(c["overlay"]), 37)
        self.assertEqual((result["files"], result["goFiles"], result["goBytes"]), (1769, 1311, 6716139))
        actual = {str(path.relative_to(dest)): path.read_bytes() for path in dest.rglob("*") if path.is_file()}
        self.assertEqual(actual, {name: data for name, (_, data) in output.items()})
        self.assertTrue(all(not path.stat().st_mode & 0o222 for path in dest.rglob("*") if path.is_file()))
        with mock.patch.object(self.r, "TREE", result["tree"]):
            self.assertTrue(self.r.verify_source(dest))
            changed = dest / "client/new00.go"
            changed.chmod(0o600)
            changed.write_bytes(b"CHANGED AFTER MATERIALIZATION")
            with self.assertRaises(self.r.Refusal): self.r.verify_source(dest)

    def test_materialize_rejects_closed_manifest_violations(self):
        original = self.plan()
        for key in ("extra", "duplicate", "runtime-command", "missing"):
            with self.subTest(key=key):
                c = copy.deepcopy(original)
                if key == "extra": c["unexpected"] = True
                elif key == "duplicate": c["commands"][1] = copy.deepcopy(c["commands"][0])
                elif key == "runtime-command": c["commands"][0]["argv"] = ["run", "./cmd/nextgo"]
                else: del c["sourceTree"]
                f = self.parent / (key + ".json")
                self.r.write_json(f, c)
                with self.assertRaises((self.r.Refusal, KeyError, ValueError)):
                    self.r.contract(f)

    def test_materialize_rejects_traversal_and_absolute_paths(self):
        for value in ("../outside", "/absolute", "a/../b", "a//b", "./a", "a/./b", ".", "", "a\x00b", "a\nb", "a\\..\\b"):
            with self.subTest(value=value), self.assertRaises((self.r.Refusal, ValueError)):
                self.r.safe_rel(value)

    def test_materialize_rejects_symlink_input_and_parent_components(self):
        checkout, c, output, git = self.synthetic_source()
        row = next(row for row in c["overlay"] if row["path"].startswith("client/"))
        target = checkout / row["fixture"]
        target.unlink()
        kept = checkout / "kept"
        kept.mkdir()
        self.put(kept / target.name, output[row["path"]][1])
        fixture_client = checkout / "tests/fixtures/offline-first-compile-overlay/client"
        shutil.rmtree(fixture_client)
        fixture_client.symlink_to(kept, target_is_directory=True)
        # Populate all client postimages behind the internal symlink: containment
        # alone must not waive strict no-symlink policy.
        for item in c["overlay"]:
            if item["path"].startswith("client/"):
                self.put(kept / Path(item["fixture"]).name, output[item["path"]][1])
        with mock.patch.object(self.r, "git_bytes", side_effect=git), mock.patch.object(self.r, "TREE", self.r.tree_id(output)):
            with self.assertRaises(self.r.Refusal):
                self.r.materialize(self.parent, checkout, self.root / "materialized", c)
        self.assertFalse((self.root / "materialized").exists())
        self.assertEqual((kept / target.name).read_bytes(), output[row["path"]][1])

    def test_materialize_enforces_file_and_total_quotas(self):
        path = self.put(self.parent / "bounded", b"abcd")
        self.assertEqual(self.r.digest_file(path, 4)["bytes"], 4)
        with self.assertRaises(self.r.Refusal): self.r.digest_file(path, 3)
        linked = self.parent / "bounded-hardlink"
        os.link(path, linked)
        with self.assertRaises(self.r.Refusal): self.r.digest_file(path, 4)
        linked.unlink()
        with mock.patch.object(self.r, "SOURCE_LIMIT", 1024):
            with self.assertRaises(self.r.Refusal): self.materialize_fixture()
        self.assertFalse((self.root / "materialized").exists())

    def test_materialize_rejects_wrong_base_tree_before_overlay(self):
        checkout, c, output, git = self.synthetic_source()
        original = copy.deepcopy(c["overlay"][0])
        for key, value in (("baseBlob", "0" * 40), ("sha256", "0" * 64),
                           ("bytes", original["bytes"] + 1), ("mode", "100777"),
                           ("fixture", "tests/fixtures/offline-first-compile-overlay/wrong.txt")):
            c["overlay"][0] = dict(original, **{key: value})
            with self.subTest(key=key), mock.patch.object(self.r, "git_bytes", side_effect=git), mock.patch.object(self.r, "TREE", self.r.tree_id(output)):
                with self.assertRaises(self.r.Refusal): self.r.materialize(self.parent, checkout, self.root / "materialized", c)
            self.assertFalse((self.root / "materialized").exists())

    def test_cleanup_joins_exact_owner_marker_path_and_inode(self):
        self.collector_fixture()
        self.assertTrue(self.r.collect(self.root)["complete"])
        sibling = self.put(self.parent / "foreign-sentinel", b"kept")
        self.r.cleanup(self.root, True)
        self.assertFalse(self.root.exists())
        self.assertEqual(sibling.read_bytes(), b"kept")

    def test_cleanup_refuses_replaced_marker_path_or_inode(self):
        original = self.r.read_json(self.root / "owner.json")
        for field, value in (("inode", -1), ("root", str(self.parent)), ("uid", -1)):
            altered = dict(original, **{field: value})
            self.r.write_json(self.root / "owner.json", altered)
            with self.assertRaises(self.r.Refusal): self.r.cleanup(self.root, True)
            self.assertTrue(self.root.exists())
        self.r.write_json(self.root / "owner.json", original)
        original_name = self.root.with_name(self.root.name + ".moved")
        self.root.rename(original_name)
        self.root.symlink_to(original_name, target_is_directory=True)
        with self.assertRaises(self.r.Refusal): self.r.cleanup(self.root, True)
        self.assertTrue(original_name.is_dir())

    def test_cleanup_refuses_unjoined_child_ownership(self):
        self.collector_fixture()
        self.r.collect(self.root)
        self.r.write_json(self.root / "active.json", {"pid": 424242, "joined": False})
        with self.assertRaises(self.r.Refusal): self.r.cleanup(self.root, True)
        self.assertTrue((self.root / "active.json").exists())

    def test_command_plan_exact_13_metadata_and_25_compile(self):
        c, calls, tools, failure, report = self.fake_run()
        self.assertIsNone(failure)
        self.assertEqual((len(c["metadataCommands"]), len(c["commands"]), len(calls), len(tools)), (13, 25, 38, 2))
        for row, call in zip(c["metadataCommands"]+c["commands"], calls):
            self.assertEqual(call[0], [str(self.sdk / "bin/go")] + [self.r.substitute(x, self.root, self.sdk) for x in row["argv"]])
            self.assertEqual(call[1], self.root / "source")
            self.assertEqual(call[2]["GOTOOLCHAIN"], "local")
            self.assertEqual(call[2]["GOPROXY"], "off")
        self.assertEqual((report["completedCommands"], report["artifactCount"], report["testsExecuted"]), (38, 25, 0))
        self.assertFalse(report["ready"] or report["nativeQualified"] or report["runtimeQualified"])

    def test_command_plan_refuses_gate_b_native_and_browser_actions(self):
        original = self.plan()
        for changes in ({"gate": "B"}, {"GateBImplemented": True}):
            c = dict(original, **changes)
            f = self.parent / "wrong-gate.json"
            self.r.write_json(f, c)
            with self.assertRaises(self.r.Refusal): self.r.contract(f)
        c = copy.deepcopy(original)
        c["commands"][0]["argv"] = ["test", "-tags=nextgo_native_browser", "./client"]
        self.r.write_json(self.parent / "runtime.json", c)
        with self.assertRaises(self.r.Refusal): self.r.contract(self.parent / "runtime.json")
        with self.assertRaises(self.r.Refusal): self.r.run(self.root, c)
        self.denied[0].assert_not_called()

    def test_command_failure_stops_once_at_ordinal_15(self):
        _, calls, _, failure, report = self.fake_run(fail_at=15)
        self.assertIsNotNone(failure)
        self.assertEqual(len(calls), 15)
        self.assertEqual(report["status"], "INCOMPLETE")
        self.assertEqual(report["completedCommands"], 15)
        proofs = self.r.read_json(self.root / "meta/command-proofs.json")
        self.assertEqual(proofs[-1]["error"], "synthetic failure")
        self.assertEqual(proofs[-1]["ordinal"], 15)

    def test_command_deadline_stops_once_at_ordinal_15(self):
        with self.assertRaises(self.r.Refusal):
            self.r.supervise(["synthetic-go"], self.root, {}, self.root, {"timeoutMS": 1}, time.time()-1)
        self.denied[0].assert_not_called()
        _, launch, _, failure, proof = self.supervisor("deadline", True)
        self.assertIsNotNone(failure)
        self.assertEqual(launch.call_count, 1)
        self.assertTrue(proof["joined"])
        self.assertFalse((self.root / "active.json").exists())
        _, calls, _, failure, report = self.fake_run(fail_at=15, fail_message="command/whole-job deadline")
        self.assertEqual(len(calls), 15)
        self.assertIsNotNone(failure)
        self.assertEqual(report["status"], "INCOMPLETE")

    def test_output_and_disk_limits_stop_before_next_admission(self):
        for mode in ("output", "disk"):
            with self.subTest(mode=mode):
                _, launch, _, failure, proof = self.supervisor(mode, True)
                self.assertIsNotNone(failure)
                self.assertEqual(launch.call_count, 1)
                self.assertTrue(proof["joined"])
                if mode == "output": self.assertLessEqual(proof["stdoutBytes"], 2)
        _, calls, _, failure, _ = self.fake_run(fail_at=14, fail_message="command output cap")
        self.assertEqual(len(calls), 14)
        self.assertIsNotNone(failure)

    def test_cancellation_preserves_unjoined_child_without_rerun(self):
        reaped = FakeChild(code=0)
        with mock.patch.object(self.r.os, "killpg") as signals:
            self.assertEqual(self.r.stop_group(reaped), [])
            signals.assert_not_called()  # no numeric-PG signal after leader reap.
        _, launch, _, failure, proof = self.supervisor("cancel", False)
        self.assertIsNotNone(failure)
        self.assertEqual(launch.call_count, 1)
        self.assertFalse(proof["joined"])
        self.assertTrue((self.root / "active.json").exists())
        with self.assertRaises(self.r.Refusal): self.r.cleanup(self.root, True)
        (self.root / "active.json").unlink()  # TESTONLY discard fake process state.
        _, calls, _, failure, report = self.fake_run(fail_at=14, joined=False)
        self.assertEqual(len(calls), 14)
        self.assertIsNotNone(failure)
        self.assertTrue(report["childOutcomeUnknown"])
        with self.assertRaises(self.r.Refusal): self.r.cleanup(self.root, True)

    def test_toolexec_forwards_exact_owned_tool_argv_transparently(self):
        args, launch, code = self.sdk_tool(tail=["spaces here", ";literal", "$(never)", "x" * 160, "--", "-o", str(self.root / "dist/x.o")])
        self.assertEqual(code, 0)
        self.assertEqual(launch.call_count, 1)
        self.assertEqual(launch.call_args.args[0], args)
        self.assertNotIn("shell", launch.call_args.kwargs)
        rows = self.r.tool_receipts(self.root)
        self.assertEqual(rows[0]["argvSHA256"], hashlib.sha256(self.r.encode(args[1:])).hexdigest())
        self.assertEqual(rows[0]["argvCount"], len(args)-1)
        self.assertEqual(rows[0]["argvPreview"], [x[:96] for x in args[1:17]])
        self.assertTrue(rows[0]["argvPreviewTruncated"])
        self.assertEqual(rows[0]["purpose"], "tool-operation")
        self.assertEqual(rows[0]["before"], self.r.digest_file(Path(args[0]), 128 << 20))

    def test_toolexec_requires_exact_owned_tool_identity_and_sdk_hash(self):
        tool = self.sdk / "pkg/tool/linux_amd64/compile"
        tool.write_bytes(b"MUTATED SYNTHETIC TOOL")
        with self.assertRaises(self.r.Refusal): self.r.tool(self.root, [str(tool)])
        outside = self.put(self.parent / "compile", elf(), 0o700)
        with self.assertRaises(self.r.Refusal): self.r.tool(self.root, [str(outside)])
        tool.unlink()
        tool.symlink_to(outside)
        with self.assertRaises(self.r.Refusal): self.r.tool(self.root, [str(tool)])

    def test_toolexec_rejects_unsafe_environment_and_flag_overrides(self):
        for key, value in (("GOTOOLCHAIN", "auto"), ("GOFLAGS", "-toolexec=/tmp/foreign"), ("GOPROXY", "https://invalid.example"), ("GOOS", "wasm"), ("CGO_ENABLED", "linux")):
            with self.subTest(key=key), self.assertRaises(self.r.Refusal):
                self.r.child_env(self.root, self.sdk, {key: value})
        env = self.r.child_env(self.root, self.sdk, {"GOOS": "js", "GOARCH": "wasm", "CGO_ENABLED": "0"})
        self.assertEqual((env["GOOS"], env["GOARCH"], env["CGO_ENABLED"]), ("js", "wasm", "0"))
        self.assertNotIn("LD_PRELOAD", env)

    def test_toolexec_receipts_obey_closed_schema_and_bounds(self):
        self.sdk_tool()
        path = next((self.root / "receipts").iterdir())
        row = self.r.read_json(path)
        original = copy.deepcopy(row)
        for key, value in (("unexpected", True), ("returned", False),
                           ("argvSHA256", "g" * 64), ("argvCount", True),
                           ("argvPreview", {"wrong": "shape"}),
                           ("argvPreviewTruncated", "false"),
                           ("importPath", "x" * 513), ("exitCode", False)):
            row = dict(original, **{key: value})
            self.r.write_json(path, row)
            with self.subTest(key=key), self.assertRaises(self.r.Refusal):
                self.r.tool_receipts(self.root)
        self.r.write_json(path, original)
        self.put(self.root / "receipts/0001.json.new", b"partial")
        with self.assertRaises(self.r.Refusal): self.r.tool_receipts(self.root)
        partial = self.r.tool_receipts(self.root, allow_partial=True)
        self.assertTrue(any(row.get("returned") is False and row.get("error") for row in partial))
        with mock.patch.object(self.r, "RECEIPT_LIMIT", 1):
            with self.assertRaises(self.r.Refusal):
                self.r.tool(self.root, [str(self.sdk / "pkg/tool/linux_amd64/link")])

    def test_artifact_headers_accept_only_expected_elf_wasm_and_archive(self):
        for kind, data in (("native", elf()), ("wasm", b"\x00asm\x01\x00\x00\x00"), ("archive", b"!<arch>\n")):
            path = self.put(self.parent / kind, data)
            result = self.r.artifact(path, kind)
            self.assertFalse(result["executed"])
            self.assertEqual(result["sha256"], hashlib.sha256(data).hexdigest())
            path.write_bytes(data[:3])
            with self.assertRaises(self.r.Refusal): self.r.artifact(path, kind)
        path = self.put(self.parent / "unknown", elf())
        with self.assertRaises(self.r.Refusal): self.r.artifact(path, "unknown")

    def test_compiled_outputs_are_never_executed(self):
        c, calls, tools, failure, report = self.fake_run()
        self.assertIsNone(failure)
        self.assertEqual(len(calls), 38)
        outputs = {str(Path(self.r.substitute(row["artifact"], self.root, self.sdk))) for row in c["commands"]}
        self.assertTrue(all(call[0][0] == str(self.sdk / "bin/go") and call[0][0] not in outputs for call in calls))
        self.assertTrue(all(args[0].startswith(str(self.sdk / "pkg/tool/linux_amd64") + "/") for args, _, _ in tools))
        self.assertEqual(report["testsExecuted"], 0)
        receipts = self.r.read_json(self.root / "meta/artifact-or-execution-receipts.json")
        self.assertTrue(all(row["executed"] is False for row in receipts["artifacts"]))

    def test_collector_closed_allowlist_refuses_extra_outputs(self):
        self.collector_fixture()
        self.put(self.root / "meta/secret.json", b"MUST NOT COLLECT")
        self.put(self.root / "logs/unknown.stdout", b"MUST NOT COLLECT")
        result = self.r.collect(self.root)
        self.assertTrue(result["complete"])
        names = {row["name"] for row in result["files"]}
        self.assertEqual(len(names), 58)
        self.assertNotIn("secret.json", names)
        self.assertFalse((self.evidence / "secret.json").exists())
        self.assertFalse((self.evidence / "unknown.stdout.gz").exists())

    def test_collector_refuses_symlink_traversal_and_owner_swap(self):
        self.collector_fixture()
        outside = self.put(self.parent / "outside", b"OUTSIDE SENTINEL")
        path = self.root / "meta" / self.r.METADATA[0]
        path.unlink()
        path.symlink_to(outside)
        result = self.r.collect(self.root)
        self.assertFalse(result["complete"])
        self.assertEqual(outside.read_bytes(), b"OUTSIDE SENTINEL")
        self.assertFalse((self.evidence / self.r.METADATA[0]).exists())
        state = self.r.read_json(self.root / "state.json")
        fresh_stage = self.parent / "nextgo-first-compile-evidence.traversal"
        fresh_stage.mkdir(mode=0o700)
        state.update({"evidence": str(fresh_stage), "evidenceDev": fresh_stage.stat().st_dev, "evidenceInode": -1})
        self.r.write_json(self.root / "state.json", state)
        with self.assertRaises(self.r.Refusal): self.r.collect(self.root)
        self.assertEqual(list(fresh_stage.iterdir()), [])
        state["evidenceInode"] = fresh_stage.stat().st_ino
        state["compileIDs"][0] = "../outside"
        self.r.write_json(self.root / "state.json", state)
        self.put(self.parent / "outside.stdout.gz", b"UNTOUCHED SENTINEL")
        with self.assertRaises(self.r.Refusal): self.r.collect(self.root)
        self.assertEqual((self.parent / "outside.stdout.gz").read_bytes(), b"UNTOUCHED SENTINEL")

    def test_collector_enforces_gzip_file_and_total_byte_limits(self):
        self.collector_fixture()
        raw = (self.root / "logs" / (self.r.COMPILE_IDS[0] + ".stdout")).read_bytes()
        with mock.patch.object(self.r, "UPLOAD_FILE_LIMIT", 1), mock.patch.object(self.r, "COLLECT_LIMIT", 65536 + 2):
            result = self.r.collect(self.root)
        self.assertFalse(result["complete"])
        self.assertLessEqual(result["bytes"], 2)
        self.assertTrue(any("omitted" in row for row in result["files"]))
        self.assertEqual((self.root / "logs" / (self.r.COMPILE_IDS[0] + ".stdout")).read_bytes(), raw)
        # Collector produces gzip; it never accepts/decompresses an input archive.
        self.assertFalse(any(path.name.endswith(".a") for path in self.evidence.iterdir()))

    def test_report_unknown_partial_missing_or_malformed_cannot_pass(self):
        def corrupt():
            path = sorted((self.root / "receipts").iterdir())[0]
            row = self.r.read_json(path)
            row["returned"] = False
            row["exitCode"] = 0
            self.r.write_json(path, row)
        _, calls, _, failure, report = self.fake_run(mutate_receipts=corrupt)
        self.assertEqual(len(calls), 38)
        self.assertIsNotNone(failure)
        self.assertEqual(report["status"], "INCOMPLETE")
        self.assertFalse(report["ready"] or report["runtimeQualified"] or report["nativeQualified"])

    def test_report_pass_requires_all_38_known_receipts_and_joined_children(self):
        _, calls, tools, failure, report = self.fake_run()
        self.assertIsNone(failure)
        self.assertEqual((len(calls), len(tools)), (38, 2))
        self.assertEqual(report["status"], "DEFAULT_SOURCE_COMPILE_LINK_ONLY_INSTALLED_SDK_DEBUG")
        self.assertFalse(report["childOutcomeUnknown"])
        self.assertEqual((report["completedCommands"], report["artifactCount"]), (38, 25))
        self.assertFalse(report["archiveAuthenticityQualified"] or report["GateBImplemented"] or report["sourceReady"] or report["ready"])
        with self.assertRaises(self.r.Refusal): self.r.run(self.root, self.plan())
        self.assertEqual(len(calls), 38)  # replay never re-enters fake supervisor.
        self.denied[0].assert_not_called()


    def test_actual_environment_report_off_reaches_next_metadata_command(self):
        self.assertEqual(len(ACTUAL_ENVIRONMENT_STDOUT), 699)
        self.assertEqual(hashlib.sha256(ACTUAL_ENVIRONMENT_STDOUT).hexdigest(), ACTUAL_ENVIRONMENT_STDOUT_SHA256)
        original = json.loads(ACTUAL_ENVIRONMENT_STDOUT)
        self.assertEqual((original["GOWORK"], original["GOENV"]), ("off", ""))
        root, calls, submitted, report = self.actual_environment_run()
        self.assertEqual(calls, ["go-version", "go-environment", "graph-native"])
        self.assertEqual(report["completedCommands"], 3)
        self.assertEqual(report["error"], "failed/unknown command; bounded compiler output retained")
        stored = self.r.read_json(root / "meta/environment.json")
        self.assertEqual(stored, json.loads(submitted))
        rebound = {"GOROOT", "GOCACHE", "GOMODCACHE", "GOTMPDIR", "GOFLAGS"}
        self.assertEqual({key: value for key, value in stored.items() if key not in rebound},
                         {key: value for key, value in original.items() if key not in rebound})
        proofs = self.r.read_json(root / "meta/command-proofs.json")
        self.assertEqual(proofs[2]["error"], "stop after environment guard")

    def test_actual_environment_report_refuses_empty_or_path_gowork(self):
        for value in ("", "/tmp/unexpected-workspace/go.work"):
            with self.subTest(GOWORK=value):
                root, calls, submitted, report = self.actual_environment_run({"GOWORK": value})
                self.assertEqual(calls, ["go-version", "go-environment"])
                self.assertEqual(report["completedCommands"], 2)
                self.assertEqual(report["error"], "actual Go environment")
                self.assertFalse((root / "meta/environment.json").exists())
                proof = self.r.read_json(root / "meta/command-proofs.json")[1]
                self.assertEqual(proof["stdout"]["sha256"], hashlib.sha256(submitted).hexdigest())
                self.assertEqual(json.loads(proof["stdout"]["prefix"])["GOWORK"], value)

    def test_actual_environment_report_goenv_still_requires_empty(self):
        for value in ("off", "/tmp/unexpected-goenv"):
            with self.subTest(GOENV=value):
                root, calls, submitted, report = self.actual_environment_run({"GOENV": value})
                self.assertEqual(calls, ["go-version", "go-environment"])
                self.assertEqual(report["completedCommands"], 2)
                self.assertEqual(report["error"], "actual Go environment")
                self.assertFalse((root / "meta/environment.json").exists())
                self.assertEqual(json.loads(submitted)["GOENV"], value)

    def test_actual_environment_report_preserves_other_guard_rejections(self):
        for key, value in (("CGO_ENABLED", "1"), ("GOTOOLCHAIN", "auto"),
                           ("GOVERSION", "go0.0.0"), ("GOOS", "windows"),
                           ("GOARCH", "arm64"), ("GOHOSTOS", "windows"),
                           ("GOHOSTARCH", "arm64"), ("GOFLAGS", ""),
                           ("GOROOT", "/tmp/unexpected-sdk"), ("GOCACHE", "/tmp/unexpected-cache"),
                           ("GOMODCACHE", "/tmp/unexpected-modules"), ("GOTMPDIR", "/tmp/unexpected-tmp")):
            with self.subTest(field=key):
                root, calls, submitted, report = self.actual_environment_run({key: value})
                self.assertEqual(calls, ["go-version", "go-environment"])
                self.assertEqual(report["completedCommands"], 2)
                self.assertEqual(report["error"], "actual Go environment")
                self.assertFalse((root / "meta/environment.json").exists())
                self.assertEqual(json.loads(submitted)[key], value)

    def test_cadence_209ms_scan_avoids_extra_idle_wait(self):
        # Saved259ms is a reported scalar only; the actual cause split stays UNKNOWN.
        self.assertEqual(ACTUAL_DISK_CADENCE_WITNESS["diskMaximumObservedSampleIntervalMS"], 259)
        self.assertEqual(ACTUAL_DISK_CADENCE_WITNESS["actualCauseSplit"], "UNKNOWN")
        self.assertAlmostEqual(1000 * (0.209 + 0.050), 259)
        proof, scans, waits, pipe_reads = self.cadence_supervisor((0.209, 0.0))
        self.assertIsNone(proof["error"])
        self.assertEqual((proof["exitCode"], proof["diskObservedPeakBytes"], pipe_reads), (0, 11932091, 2))
        after_first_scan = [row for row in waits if row["scans"] == 1]
        self.assertEqual(len(after_first_scan), 1)
        self.assertEqual(after_first_scan[0]["timeout"], 0.0)
        self.assertEqual(len(scans), 2)
        self.assertAlmostEqual(scans[1] - scans[0], 0.209)
        self.assertLessEqual(proof["diskMaximumObservedSampleIntervalMS"], 209)
        self.assertFalse(proof["diskHardQuota"])

    def test_cadence_fast_scan_waits_use_remaining_target_and_50ms_cap(self):
        for scan_seconds, expected_wait in ((0.010, 0.050), (0.070, 0.030)):
            with self.subTest(scan_seconds=scan_seconds):
                proof, scans, waits, pipe_reads = self.cadence_supervisor((scan_seconds, 0.0))
                self.assertIsNone(proof["error"])
                self.assertEqual(pipe_reads, 2)
                after_first_scan = [row for row in waits if row["scans"] == 1]
                self.assertGreaterEqual(len(after_first_scan), 1)
                self.assertAlmostEqual(after_first_scan[0]["timeout"], expected_wait)
                self.assertAlmostEqual(scans[1] - scans[0], 0.100)
                self.assertLessEqual(proof["diskMaximumObservedSampleIntervalMS"], 100)

    def test_cadence_walk_over_250ms_still_refuses(self):
        proof, scans, waits, pipe_reads = self.cadence_supervisor((0.251,))
        self.assertEqual(proof["error"], "disk inventory exceeded sample interval")
        self.assertEqual((len(scans), pipe_reads), (1, 0))
        self.assertFalse(any(row["scans"] >= 1 for row in waits))

    def test_cadence_scheduling_delay_over_250ms_still_refuses(self):
        proof, scans, waits, pipe_reads = self.cadence_supervisor((), delayed_first_wait=0.201)
        # Requested50ms plus synthetic201ms scheduler delay is251ms; no walk occurs.
        self.assertEqual(proof["error"], "disk sampling interval exceeded")
        self.assertEqual((len(scans), len(waits), pipe_reads), (0, 1, 0))
        self.assertAlmostEqual(waits[0]["timeout"], 0.050)
        self.assertGreaterEqual(proof["diskMaximumObservedSampleIntervalMS"], 250)

    def test_cadence_command_and_whole_job_deadlines_remain_active(self):
        for command_ms, whole_ms in ((80, 10000), (10000, 80)):
            with self.subTest(command_ms=command_ms, whole_ms=whole_ms):
                proof, scans, waits, pipe_reads = self.cadence_supervisor((),
                    command_timeout_ms=command_ms, whole_timeout_ms=whole_ms)
                self.assertEqual(proof["error"], "command/whole-job deadline")
                self.assertEqual((len(scans), pipe_reads), (0, 0))
                self.assertEqual(len(waits), 2)


if __name__ == "__main__":
    unittest.main()
