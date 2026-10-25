"""Offline tests only: no AWS process, credentials, network, or cloud resources."""
import contextlib
import copy
import datetime as dt
import hashlib
import io
import json
from pathlib import Path
import stat
import struct
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import zipfile

import qualify as q


def state():
    owner, sha = "a" * 32, "b" * 64
    return {"version": 1, "owner": owner, "stack": "nextgo-qual-offline", "account": "123456789012", "region": "us-west-2",
            "stackArn": "arn:aws:cloudformation:us-west-2:123456789012:stack/nextgo-qual-offline/unique-id",
            "zipSHA256": sha, "templateSHA256": q.digest(q.TEMPLATE.read_bytes()), "key": f"artifacts/{owner}/bootstrap-{sha}.zip"}


def description(value, status="CREATE_COMPLETE"):
    return {"Stacks": [{"StackName": value["stack"], "StackId": value["stackArn"], "StackStatus": status,
                        "Tags": [{"Key": q.OWNER_TAG, "Value": value["owner"]}, {"Key": q.KIND_TAG, "Value": q.KIND}]}]}


class FakeAWS:
    def __init__(self, replies):
        self.replies, self.calls = list(replies), []

    def command(self, args, **_):
        self.calls.append(args)
        expected, reply = self.replies.pop(0)
        if args[:2] != expected:
            raise AssertionError((args[:2], expected))
        return reply(args) if callable(reply) else reply


class OfflineQualificationTests(unittest.TestCase):
    def test_default_plan_never_invokes_aws_or_reads_credentials(self):
        output = io.StringIO()
        with patch.object(q.subprocess, "Popen", side_effect=AssertionError("must stay offline")), patch.dict(q.os.environ, {"AWS_ENDPOINT_URL": "https://forbidden.invalid", "AWS_SECRET_ACCESS_KEY": "SYNTHETIC_SECRET"}), contextlib.redirect_stdout(output):
            self.assertEqual(q.main([]), 0)
        value = json.loads(output.getvalue())
        self.assertEqual(value["cloudCalls"], 0)
        self.assertNotIn("SYNTHETIC_SECRET", output.getvalue())

    def test_mutation_without_explicit_execute_is_refused(self):
        with patch.object(q.subprocess, "Popen", side_effect=AssertionError("no cloud process")):
            with self.assertRaises(q.Refused):
                q.main(["cleanup"])

    def test_environment_rejects_endpoint_and_ca_injection_and_drops_ambient_auth(self):
        for name in ("AWS_ENDPOINT_URL", "AWS_ENDPOINT_URL_STS", "AWS_CA_BUNDLE", "AWS_CONFIG_FILE", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "SSL_CERT_FILE", "SSLKEYLOGFILE", "BOTO_CONFIG"):
            with self.assertRaises(q.Refused):
                q.safe_environment({name: "synthetic", "PATH": "/bin"})
        safe = q.safe_environment({"AWS_ACCESS_KEY_ID": "SECRET", "AWS_SECRET_ACCESS_KEY": "SECRET", "AWS_PROFILE": "ambient", "PATH": "/bin"})
        self.assertNotIn("AWS_ACCESS_KEY_ID", safe)
        self.assertNotIn("AWS_SECRET_ACCESS_KEY", safe)
        self.assertEqual(safe["AWS_EC2_METADATA_DISABLED"], "true")

    def test_every_stack_ownership_binding_is_required(self):
        value = state()
        self.assertEqual(q.owned_stack(description(value), value)["StackId"], value["stackArn"])
        for mutate in (lambda x: x["Stacks"][0].update(StackName="foreign"),
                       lambda x: x["Stacks"][0].update(StackId=value["stackArn"].replace("us-west-2", "us-east-1")),
                       lambda x: x["Stacks"][0].update(StackId=value["stackArn"].replace("123456789012", "999999999999")),
                       lambda x: x["Stacks"][0].update(StackId=value["stackArn"] + "-replacement"),
                       lambda x: x["Stacks"][0].update(Tags=[]),
                       lambda x: x["Stacks"][0]["Tags"][0].update(Value="c" * 32)):
            wrong = copy.deepcopy(description(value))
            mutate(wrong)
            aws = FakeAWS([(["cloudformation", "describe-stacks"], wrong)])
            with self.assertRaises(q.Refused):
                q.cleanup(aws, value)
            self.assertEqual(len(aws.calls), 1, "no deletion or S3 access after an ownership mismatch")

    def test_foreign_bucket_objects_and_replaced_artifacts_are_never_deleted(self):
        value = state()
        prefix = [(["cloudformation", "describe-stacks"], description(value)),
                  (["cloudformation", "describe-stack-resource"], {"StackResourceDetail": {"PhysicalResourceId": "owned-synthetic-bucket"}})]
        for listing, extra in (({"Contents": [{"Key": "foreign"}]}, []),
                               ({"Contents": [{"Key": value["key"]}], "IsTruncated": True}, []),
                               ({"Contents": [{"Key": value["key"]}]}, [(["s3api", "head-object"], {"Metadata": {"qualification-owner": "foreign"}})])):
            aws = FakeAWS(prefix + [(["s3api", "list-objects-v2"], listing)] + extra)
            with self.assertRaises(q.Refused):
                q.cleanup(aws, value)
            self.assertFalse(any("delete" in command[1] for command in aws.calls))

    def test_successful_cleanup_targets_only_exact_owned_key_and_arn(self):
        value = state()
        aws = FakeAWS([
            (["cloudformation", "describe-stacks"], description(value)),
            (["cloudformation", "describe-stack-resource"], {"StackResourceDetail": {"PhysicalResourceId": "owned-synthetic-bucket"}}),
            (["s3api", "list-objects-v2"], {"Contents": [{"Key": value["key"]}]}),
            (["s3api", "head-object"], {"Metadata": {"qualification-owner": value["owner"], "artifact-sha256": value["zipSHA256"]}}),
            (["s3api", "delete-object"], {}), (["cloudformation", "delete-stack"], {}),
            (["cloudformation", "describe-stacks"], description(value, "DELETE_COMPLETE"))])
        q.cleanup(aws, value)
        deletes = [command for command in aws.calls if "delete" in command[1]]
        self.assertEqual(deletes, [["s3api", "delete-object", "--bucket", "owned-synthetic-bucket", "--key", value["key"]],
                                  ["cloudformation", "delete-stack", "--stack-name", value["stackArn"]]])

    def test_zip_digest_architecture_and_root_executable_are_verified(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "synthetic.zip"
            elf = bytearray(64)
            elf[:6] = b"\x7fELF\x02\x01"
            struct.pack_into("<H", elf, 18, 62)
            entry = zipfile.ZipInfo("bootstrap")
            entry.external_attr = (stat.S_IFREG | 0o755) << 16
            with zipfile.ZipFile(path, "w") as archive:
                archive.writestr(entry, elf)
            sha = q.digest(path.read_bytes())
            self.assertEqual(q.inspect_zip(path, sha, "x86_64"), (q.digest(elf), path.read_bytes()))
            for checksum, architecture in (("0" * 64, "x86_64"), (sha, "arm64")):
                with self.assertRaises(q.Refused):
                    q.inspect_zip(path, checksum, architecture)
            with zipfile.ZipFile(path, "a") as archive:
                archive.writestr("other-file", b"unexpected")
            with self.assertRaises(q.Refused):
                q.inspect_zip(path, q.digest(path.read_bytes()), "x86_64")

    def test_sigv4_binds_host_region_path_query_and_session(self):
        credentials = {"AccessKeyId": "SYNTHETICACCESSKEY", "SecretAccessKey": "SYNTHETICSECRETKEY", "SessionToken": "SYNTHETICSESSION"}
        now = dt.datetime(2026, 1, 2, 3, 4, 5, tzinfo=dt.timezone.utc)
        sign = lambda host, region, path: q.signed_headers(credentials, host, region, path, {}, now)
        first = sign("abc.lambda-url.us-west-2.on.aws", "us-west-2", "/resource?b=2&a=1")
        self.assertEqual(first, sign("abc.lambda-url.us-west-2.on.aws", "us-west-2", "/resource?a=1&b=2"))
        self.assertIn("20260102/us-west-2/lambda/aws4_request", first["authorization"])
        self.assertEqual(first["x-amz-security-token"], "SYNTHETICSESSION")
        for host, region, path in (("other.lambda-url.us-west-2.on.aws", "us-west-2", "/resource?a=1&b=2"),
                                   ("abc.lambda-url.us-west-2.on.aws", "us-east-1", "/resource?a=1&b=2"),
                                   ("abc.lambda-url.us-west-2.on.aws", "us-west-2", "/other?a=1&b=2")):
            self.assertNotEqual(first["authorization"], sign(host, region, path)["authorization"])

    def test_original_zip_replacement_cannot_change_uploaded_snapshot(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            original, output = directory / "original.zip", directory / "owned"
            output.mkdir(mode=0o700)
            elf = bytearray(64)
            elf[:6] = b"\x7fELF\x02\x01"
            struct.pack_into("<H", elf, 18, 62)
            entry = zipfile.ZipInfo("bootstrap")
            entry.external_attr = (stat.S_IFREG | 0o755) << 16
            with zipfile.ZipFile(original, "w") as archive:
                archive.writestr(entry, elf)
            expected = q.digest(original.read_bytes())
            _, encoded = q.inspect_zip(original, expected, "x86_64")
            snapshot = output / "artifact.zip"
            q.freeze_artifact(snapshot, encoded)
            q.freeze_artifact(output / "stack.json", q.TEMPLATE.read_bytes())
            original.write_bytes(b"A later build replaced the original ZIP")
            value = state()
            value.update(zipSHA256=expected, architecture="x86_64", invokerArn="arn:aws:iam::123456789012:role/synthetic")
            created = description(value)
            created["Stacks"][0]["Outputs"] = [{"OutputKey": "Bucket", "OutputValue": "owned-synthetic-bucket"}]
            def inspect_upload(command):
                uploaded = Path(command[command.index("--body") + 1])
                self.assertEqual(uploaded, snapshot.resolve())
                self.assertEqual(q.digest(uploaded.read_bytes()), expected)
                self.assertNotEqual(uploaded.read_bytes(), original.read_bytes())
                return {}
            def inspect_template(command):
                supplied = command[command.index("--template-body") + 1]
                self.assertEqual(supplied, "file://" + str((output / "stack.json").resolve()))
                self.assertEqual(q.digest(Path(supplied[7:]).read_bytes()), value["templateSHA256"])
                return {"StackId": value["stackArn"]} if command[1] == "create-stack" else {}
            aws = FakeAWS([
                (["cloudformation", "describe-stacks"], None),
                (["cloudformation", "create-stack"], inspect_template),
                (["cloudformation", "describe-stacks"], created),
                (["s3api", "put-object"], inspect_upload),
                (["cloudformation", "update-stack"], inspect_template),
                (["cloudformation", "describe-stacks"], description(value, "UPDATE_COMPLETE"))])
            changed_template = directory / "later-template.json"
            changed_template.write_text('{"unapproved":"later edit"}')
            with patch.object(q, "TEMPLATE", changed_template):
                q.provision(aws, SimpleNamespace(zip=original, out=output), value, lambda: None)
            self.assertFalse(aws.replies)
            with self.assertRaises(FileExistsError):
                q.freeze_artifact(snapshot, b"must never overwrite")

    def test_exact_invoker_binding_rejects_broader_same_account_roles(self):
        role = "arn:aws:iam::123456789012:role/ApprovedInvoker"
        self.assertTrue(q.principal_matches(role, "arn:aws:sts::123456789012:assumed-role/ApprovedInvoker/session"))
        for wrong in ("arn:aws:sts::123456789012:assumed-role/Admin/session", "arn:aws:sts::999999999999:assumed-role/ApprovedInvoker/session"):
            self.assertFalse(q.principal_matches(role, wrong))
        self.assertFalse(q.principal_matches("arn:aws:iam::123456789012:role/path/ApprovedInvoker", "arn:aws:sts::123456789012:assumed-role/ApprovedInvoker/session"))
        user = "arn:aws:iam::123456789012:user/path/approved"
        self.assertTrue(q.principal_matches(user, user))
        self.assertFalse(q.principal_matches(user, user + "-other"))

    def test_expired_global_deadline_stops_before_network(self):
        with patch.object(q.time, "monotonic", return_value=100), patch.object(q.http.client, "HTTPSConnection", side_effect=AssertionError("no network")):
            with self.assertRaises(q.Refused):
                q.https_get("https://abc.lambda-url.us-west-2.on.aws/", "us-west-2", {}, "/", deadline=99)

    def test_exported_signing_identity_is_checked_before_function_urls(self):
        value = state()
        value["invokerArn"] = "arn:aws:iam::123456789012:role/ApprovedInvoker"
        result = description(value)
        result["Stacks"][0]["Outputs"] = []
        aws = FakeAWS([(["cloudformation", "describe-stacks"], result),
                       (["configure", "export-credentials"], {"AccessKeyId": "SYNTHETICACCESSKEY", "SecretAccessKey": "SYNTHETICSECRETKEY"})])
        aws.deadline = 123
        wrong = b"<GetCallerIdentityResponse><Account>123456789012</Account><Arn>arn:aws:sts::123456789012:assumed-role/Admin/session</Arn></GetCallerIdentityResponse>"
        with patch.object(q, "https_get", return_value=({"status": 200}, wrong, None)) as network:
            with self.assertRaises(q.Refused):
                q.exercise(aws, value, {"requests": []})
        self.assertEqual(network.call_count, 1)
        self.assertEqual(network.call_args.kwargs, {"deadline": 123, "service": "sts"})

    def test_windows_cloud_execution_is_rejected_before_subprocesses(self):
        args = SimpleNamespace(execute=True, account="123456789012", region="us-west-2", profile="synthetic", budget_usd=1, action="cleanup", keep=False)
        with patch.object(q.os, "name", "nt"):
            with self.assertRaises(q.Refused):
                q.validate_options(args)

    def test_preflight_failure_writes_redacted_proof_and_never_attempts_cleanup(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "state.json"
            q.write_private(path, state())
            class RejectAWS:
                def __init__(self, _): self.calls = 0
                def preflight(self): raise RuntimeError("SYNTHETIC_SECRET_CREDENTIAL")
            output = io.StringIO()
            with patch.object(q, "AWS", RejectAWS), patch.object(q, "cleanup", side_effect=AssertionError("must not delete")), contextlib.redirect_stdout(output):
                code = q.main(["exercise", "--execute", "--profile", "synthetic", "--account", "123456789012", "--region", "us-west-2", "--budget-usd", "1", "--state", str(path)])
            proof = (path.parent / "proof.json").read_text()
            self.assertEqual(code, 1)
            self.assertNotIn("SYNTHETIC_SECRET_CREDENTIAL", proof + output.getvalue())
            self.assertEqual(json.loads(proof)["cleanup"], "preflight-refused; no cleanup attempted")

    def test_template_grants_only_logging_and_private_url_invocation(self):
        template = json.loads(q.TEMPLATE.read_text())
        resources = template["Resources"]
        policy = resources["ExecutionRole"]["Properties"]["Policies"][0]["PolicyDocument"]["Statement"]
        self.assertEqual(policy[0]["Action"], ["logs:CreateLogStream", "logs:PutLogEvents"])
        for mode in ("Buffered", "Streaming"):
            self.assertEqual(resources[mode + "URL"]["Properties"]["AuthType"], "AWS_IAM")
            self.assertEqual(resources[mode + "Logs"]["Properties"]["RetentionInDays"], 1)
            self.assertEqual(resources[mode + "Function"]["Properties"]["ReservedConcurrentExecutions"], 1)
            self.assertTrue(resources[mode + "Invoke"]["Properties"]["InvokedViaFunctionUrl"])
            self.assertNotEqual(resources[mode + "InvokeURL"]["Properties"]["Principal"], "*")

    def test_partial_redacted_proof_precedes_owned_failure_cleanup(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "state.json"
            q.write_private(path, state())
            class ApprovedAWS:
                def __init__(self, _): self.calls = 1
                def preflight(self): pass
            def confirm_cleanup(_aws, _state):
                saved = json.loads((path.parent / "proof.json").read_text())
                self.assertEqual(saved["status"], "failed")
                self.assertNotIn("SYNTHETIC_PRIVATE_BODY", json.dumps(saved))
            with patch.object(q, "AWS", ApprovedAWS), patch.object(q, "exercise", side_effect=RuntimeError("SYNTHETIC_PRIVATE_BODY")), patch.object(q, "cleanup", side_effect=confirm_cleanup), contextlib.redirect_stdout(io.StringIO()):
                code = q.main(["exercise", "--execute", "--profile", "synthetic", "--account", "123456789012", "--region", "us-west-2", "--budget-usd", "1", "--state", str(path)])
            self.assertEqual(code, 1)
            self.assertEqual(json.loads((path.parent / "proof.json").read_text())["cleanup"], "deleted-owned-stack")

    def test_later_cleanup_recovers_uncertain_create_only_with_exact_ownership(self):
        for outcome in ("owned", "foreign", "unknown"):
            with self.subTest(outcome=outcome), tempfile.TemporaryDirectory() as temporary:
                path = Path(temporary) / "state.json"
                value = state()
                uncertain = dict(value)
                uncertain.pop("stackArn")
                q.write_private(path, uncertain)
                candidate = description(value) if outcome != "unknown" else None
                if outcome == "foreign":
                    candidate["Stacks"][0]["Tags"][0]["Value"] = "f" * 32
                class RecoverAWS:
                    def __init__(self, _): self.calls = 0
                    def preflight(self): pass
                    def command(self, command, **_):
                        self.calls += 1
                        self_command = ["cloudformation", "describe-stacks", "--stack-name", value["stack"]]
                        if command != self_command: raise AssertionError(command)
                        return candidate
                with patch.object(q, "AWS", RecoverAWS), patch.object(q, "cleanup") as cleanup, contextlib.redirect_stdout(io.StringIO()):
                    code = q.main(["cleanup", "--execute", "--profile", "synthetic", "--account", "123456789012", "--region", "us-west-2", "--budget-usd", "1", "--state", str(path)])
                self.assertEqual(code, 0 if outcome == "owned" else 1)
                self.assertEqual(cleanup.call_count, 1 if outcome == "owned" else 0)
                if outcome == "owned":
                    self.assertEqual(cleanup.call_args.args[1]["stackArn"], value["stackArn"])

    def test_proof_disk_error_does_not_skip_owned_cleanup_or_leak_raw_errors(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "state.json"
            q.write_private(path, state())
            class ApprovedAWS:
                def __init__(self, _): self.calls = 0
                def preflight(self): pass
            original_write = q.write_private
            def full_disk(target, value):
                if target.name == "proof.json": raise OSError("SYNTHETIC_PRIVATE_DISK_DETAIL")
                return original_write(target, value)
            output, errors = io.StringIO(), io.StringIO()
            with patch.object(q, "AWS", ApprovedAWS), patch.object(q, "write_private", side_effect=full_disk), patch.object(q, "exercise", side_effect=RuntimeError("SYNTHETIC_PRIVATE_BODY")), patch.object(q, "cleanup") as cleanup, contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
                code = q.main(["exercise", "--execute", "--profile", "synthetic", "--account", "123456789012", "--region", "us-west-2", "--budget-usd", "1", "--state", str(path)])
            self.assertEqual(code, 1)
            cleanup.assert_called_once()
            self.assertNotIn("SYNTHETIC_PRIVATE", output.getvalue() + errors.getvalue())


if __name__ == "__main__":
    unittest.main()
