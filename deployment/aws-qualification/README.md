# Opt-in real AWS qualification scaffold

**No cloud qualification has been performed by adding this scaffold.** Its
template and orchestration have offline tests only; AWS schema acceptance,
permissions, signing, streaming behavior, actual charges and cleanup must still
be qualified in an approved account. This is neither deployment certification
nor a production authentication/operations example.

Default invocation is strictly offline and creates no files or resources:

```sh
python3 deployment/aws-qualification/qualify.py
python3 -m unittest discover -s deployment/aws-qualification -p 'test_*.py' -v
```

Review `stack.json`, `qualify.py`, the ZIP, account, region, invoker and budget
before approving cloud execution. **Do not run the following cloud commands
until that approval exists.** Cloud execution supports POSIX Linux/macOS only;
the bounded subprocess reader and deadline signal handling do not support
Windows. Python 3.10+ and an already installed AWS CLI v2
with `configure export-credentials` are required; nothing is installed here.

## Artifact and identity

Use a prebuilt, synthetic demo Linux executable matching the reviewed revision
and Go/WASM runtime generation. Follow the repository's existing Docker
`artifact` target and `provided.al2023` ZIP convention in
[DEPLOYMENT.md](../../docs/DEPLOYMENT.md). The ZIP must contain exactly one
regular executable named `bootstrap` at its root; x86_64 or arm64 must match its
ELF header. The script checks the supplied ZIP SHA-256 and records the ELF hash.
It parses exactly the bounded bytes it hashed, then freezes both that ZIP and
the reviewed template into new exclusive, owner-read-only private files. Only
those snapshots are supplied to AWS CLI, so replacing a build's original ZIP or
editing the source template during provisioning cannot change the approved code.
It never invokes Go, Docker, a compiler, or a package installer.

Use only the synthetic demonstration build, with no private prerender data,
native production secrets, external data dependencies or customer data. A ZIP
can contain secrets embedded by trusted build code; its digest is not a secret
scanner. No database, application credentials, cookie signing key or other data
access is configured by the template.

Every cloud action requires an explicit CLI profile, expected 12-digit account,
standard commercial AWS region, `--execute` and budget acknowledgement. STS
account verification precedes writes. The script rejects environment endpoint,
credential-file, legacy Boto configuration, TLS key-logging and CA overrides and removes ambient AWS credential variables
from its child environment. It rejects profile endpoint/CA/service overrides,
credential processes, metadata, web identity and SSO discovery; a bounded
`source_profile` chain is allowed. Prepare a named profile with short-lived,
preauthenticated credentials outside this scaffold. No credential file is read
directly by the Python script. AWS CLI resolves only explicitly selected
profiles when a cloud action is invoked.

The operator's existing deployment principal needs reviewed CloudFormation,
S3, Lambda, log-group and IAM role-management permissions for this isolated
stack, including `iam:PassRole` limited to the generated Lambda execution role.
This scaffold does not create a deployment administrator or attach broad
managed policies to the operator. Review those operator permissions separately.

Function URLs use **AWS_IAM**, not public access. Four resource-policy statements
grant only the supplied same-account IAM user/role ARN the two URL permissions;
`lambda:InvokeFunction` is restricted to URL invocation. Supply the IAM role ARN,
not an STS assumed-role session ARN. This small scaffold supports pathless IAM
roles and exact IAM user ARNs; role paths and federated-user identities are
rejected. The named profile used to deploy/exercise must resolve to that exact
principal, not merely the same account. Explicit exercise exports credentials into
process memory through AWS CLI, signs HTTPS GETs using stdlib SigV4, and never
places credentials in arguments, files, proof or logs. Signed requests use only
the exact region's validated `*.lambda-url.<region>.on.aws` HTTPS hosts, TLS
verification and direct connections; redirects and environment proxies are not
used. A separate signed request to the fixed regional STS endpoint verifies the
identity of the actual exported key before either Function URL is contacted.
No public URL, browser credential proxy or external provider is installed.

## Reviewed run and cleanup

Example placeholders below are deliberately incomplete:

```sh
python3 deployment/aws-qualification/qualify.py run --execute \
  --profile APPROVED_NAMED_PROFILE --account APPROVED_ACCOUNT_ID \
  --region APPROVED_REGION --stack nextgo-qual-UNIQUE_LOWERCASE_NAME \
  --invoker-arn arn:aws:iam::APPROVED_ACCOUNT_ID:role/APPROVED_ROLE \
  --architecture x86_64 --zip /ABSOLUTE/PATH/TO/SYNTHETIC.zip \
  --zip-sha256 REVIEWED_64_HEX_DIGEST --budget-usd APPROVED_AMOUNT \
  --out /ABSOLUTE/NEW/PRIVATE/OUTPUT_DIRECTORY
```

`run` performs one isolated two-stage deployment: create the owned private S3
bucket/log groups/log-only execution role; upload one exact inspected ZIP; update
the **same stack** with buffered and streaming Lambda functions/URLs; verify the
signing identity with one STS HTTPS request and exercise fourteen synthetic GETs;
then clean up by default. There are no automatic HTTP
retries or mutation replay. New stack names are required; existing stacks are
never adopted or updated. Code uses `provided.al2023`, 512 MiB, ten-second
timeouts, one reserved concurrent execution per function, and 1-day log retention.
The default work deadline is 15 minutes, with up to five additional cleanup
minutes, bounded CLI commands/polls and bounded HTTPS request/response sizes.
The approved work deadline also applies during HTTPS probes, with a POSIX timer
covering the run and a separate bounded cleanup interval. SIGTERM/interrupts
enter the owned cleanup path; a force-killed process cannot perform cleanup.

The budget argument records explicit approval; **it is not an AWS billing cap**.
S3, CloudFormation-related resources, logs, requests and execution can incur
charges. Low account concurrency quotas may reject reserved concurrency.
Termination, unavailable AWS APIs or a killed client can leave resources and
charges behind. There is no scheduler or out-of-process janitor. `--keep`
intentionally retains resources on success **and failure**, for review; charges
continue until owned cleanup succeeds. Do not choose it casually.

Private `state.json` retains the exact stack identity and ownership nonce for
cleanup. Private `artifact.zip` and `stack.json` retain the frozen upload inputs.
`proof.json` contains hashes (including the scaffold's source), synthetic paths, selected HTTP metadata,
timings, approval amount and cleanup outcome, never response bodies, credentials,
cookies or raw CLI errors. Account/stack/endpoint identifiers are hashed in
proof. Keep state private; do not publish it as the redacted proof artifact.
Partial proof is saved before failure cleanup. A proof-write failure is reported
and still attempts owned cleanup. A failed cleanup makes the overall result fail and is reported
explicitly and must be investigated; a successful script exit is not a billing
or production-security guarantee.

To explicitly repeat exercise against an intentionally retained stack, use
`exercise --execute --state /PRIVATE/state.json` with the same profile/account/
region/budget arguments. It also cleans up by default; `--keep` has the same
intentional retention meaning. To clean up without exporting signing credentials:

```sh
python3 deployment/aws-qualification/qualify.py cleanup --execute \
  --profile APPROVED_NAMED_PROFILE --account APPROVED_ACCOUNT_ID \
  --region APPROVED_REGION --budget-usd APPROVED_AMOUNT \
  --state /PRIVATE/OUTPUT/state.json
```

Cleanup verifies exact stack ARN/account/region and **both** ownership tags,
resolves the bucket through that stack's resource record, and refuses foreign
objects or changed artifact metadata. Only the exact recorded artifact key and
exact owned stack are deleted: no wildcard search, recursive bucket removal,
prefix deletion or owner-tag adoption. If state/template ownership does not
match, review manually rather than relaxing checks. Removing a template or local
state file does not delete AWS resources.

## Meaning of future proof

The checks cover private buffered/streaming Function URL HTTPS transport,
meaningful resource SSR, navigation generation identity, public JSON, PNG,
versioned WASM, and separate early/deferred streamed publication. They do not
exercise a real browser, custom domains, API Gateway mappings, CDN behavior,
cold-start distributions, platform freeze scheduling, distributed state,
rollout/rollback, sustained load, durable operations or production authentication.
All existing release gates remain open until their own evidence is reviewed.

Protocol references used when preparing this scaffold:
[Function URL access](https://docs.aws.amazon.com/lambda/latest/dg/urls-auth.html),
[streaming URL modes](https://docs.aws.amazon.com/lambda/latest/dg/config-rs-invoke-furls.html),
[CloudFormation URL](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/aws-resource-lambda-url.html),
[URL permissions](https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/aws-resource-lambda-permission.html),
[explicit profile credential export](https://docs.aws.amazon.com/cli/latest/reference/configure/export-credentials.html).
