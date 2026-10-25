# Security reporting

Next.go is experimental and has no supported release series, published security
maintenance window, response SLA or monitored private security contact recorded
in this repository. No independently qualified production-release security
guarantee follows from the existing focused reviews/tests. See
[compatibility policy](docs/COMPATIBILITY.md) and [release gates](docs/PARITY.md).

To report a suspected vulnerability, first check the canonical repository's
**Security → Report a vulnerability** entry. If GitHub private vulnerability
reporting is enabled there, use that private flow. Its availability and response
times are not established by this file. If that entry is absent and no private
contact is published, open a public issue asking for a private reporting channel
without including exploit steps, credentials, private data or sensitive details.
There is currently no verified confidential fallback; do not assume an ordinary
issue or pull request is private. A working private channel remains release work.

In a private report, include the exact framework/application commit, Go SDK,
native OS/CPU or browser/runtime, relevant configuration and feature, expected
security boundary, observed impact, and a minimal isolated reproduction with
synthetic data. State whether the failure needs a custom application handler,
untrusted input, authentication, shared-cache writer access, or deployment proxy
configuration. Remove tokens, signing keys, session cookies and production
payloads. Avoid testing other people's deployments or destructive reproductions.

Public bug reports are appropriate for nonsensitive correctness/documentation
issues. If sensitivity is uncertain, start with the private-channel request.

When handling a report, maintainers should record affected commits and exploit
conditions, validate the boundary, preserve a minimal regression test, and
describe mitigations without exposing reporter secrets. Fix notes must identify
the exact corrected revision, affected API/protocol/schema behavior, required
upgrade and rollback constraints, and any remaining risk. Do not describe an
unreleased patch as an available release. Publication timing, advisory/CVE
assignment and credit require coordination with the reporter; none is automatic
or promised. Critical safety corrections may reject unsafe inputs without a
normal deprecation interval.

Operators should pin inspected commits and supported patched SDKs, keep native
secrets outside browser/public assets, implement application authorization and
durable writes, and qualify actual ingress/runtime/cache boundaries. A framework
update cannot repair an application's leaked credential or prove a write was
rolled back. See [deployment](docs/DEPLOYMENT.md) and feature-specific contracts
for the implemented trust boundaries and limits.
