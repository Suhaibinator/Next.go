#!/usr/bin/env bash
# Check only this fixed fixture's real verbose test output. No JSON interpreter,
# network access, Go compiler, Node or browser runtime is used by this verifier.
set -euo pipefail
export LC_ALL=C
fail() { printf 'Invocation fixture proof rejected: %s\n' "$*" >&2; exit 1; }
[[ $# == 3 ]] || fail 'usage: verify-output.sh TEST_LOG PROOF_DIRECTORY ACTUAL_TEST_EXIT_CODE'
log=$1
proof=$2
[[ $3 == 0 ]] || fail "actual test executable exit code was $3"
[[ -f $log && ! -L $log ]] || fail 'actual regular test log is required'
[[ $(wc -c < "$log") -le 8388608 ]] || fail 'test log exceeds 8 MiB'
mkdir -p "$proof"
check_count() {
  local expected=$1 expression=$2 input=$3 actual
  actual=$(grep -Ec -- "$expression" "$input" || :)
  [[ $actual == "$expected" ]] || fail "expected $expected matches of $expression; observed $actual"
}
if grep -Eq -- '^[[:space:]]*--- (FAIL|SKIP):|^(FAIL|SKIP)$|\[no tests to run\]' "$log"; then
  fail 'failure, skip or empty test selection appeared'
fi
check_count 1 '^--- PASS: TestInvocationIdentityGeneratedExecutable \([0-9]+(\.[0-9]+)?s\)$' "$log"
for mode in http buffered streaming; do
  check_count 1 "^[[:space:]]*--- PASS: TestInvocationIdentityGeneratedExecutable/${mode} \\([0-9]+(\\.[0-9]+)?s\\)$" "$log"
done
check_count 4 '^[[:space:]]*--- PASS:' "$log"
check_count 1 '^PASS$' "$log"
sed -n 's/^.*INVOCATION_EXECUTABLE_OBSERVATION //p' "$log" > "$proof/observations.jsonl"
sed -n 's/^.*INVOCATION_EXECUTABLE_RECEIPT //p' "$log" > "$proof/receipts.jsonl"
[[ $(wc -l < "$proof/observations.jsonl") -eq 9 ]] || fail 'exactly nine paired response/After observations required'
[[ $(wc -l < "$proof/receipts.jsonl") -eq 3 ]] || fail 'exactly three physical cleanup receipts required'
# Go encoding/json sorts string-keyed map keys. Matching complete records rejects
# extra/missing fields and duplicates without accepting a partial grep as proof.
: > "$proof/expected-observations.jsonl"
for mode in http buffered streaming; do
  for sequence in 1 2 3; do
    invocation= gateway= retained_invocation= retained_gateway=
    if [[ $mode != http ]]; then
      case $sequence in
        1) invocation=runtime-one; gateway=gateway-alpha ;;
        2) invocation=runtime-two; gateway=gateway-beta ;;
        3) invocation=runtime-three ;;
      esac
      retained_invocation=runtime-one
      retained_gateway=gateway-alpha
    fi
    printf '{"mode":"%s","responseAndAfter":{"sequence":%s,"invocation":"%s","gateway":"%s","retainedInvocation":"%s","retainedGateway":"%s"}}\n' \
      "$mode" "$sequence" "$invocation" "$gateway" "$retained_invocation" "$retained_gateway" >> "$proof/expected-observations.jsonl"
  done
done
sort "$proof/observations.jsonl" > "$proof/actual-observations.sorted"
sort "$proof/expected-observations.jsonl" > "$proof/expected-observations.sorted"
cmp "$proof/actual-observations.sorted" "$proof/expected-observations.sorted" || fail 'actual response/After identities differ'
prefix='\{"callbackReplies":\[true,true,true\],"childExitCode":0,"childKilled":false,"childLinuxStartTicks":"[0-9]+","childPID":[1-9][0-9]*,"childWaitReturned":true,"cleanupBudgetSeconds":20,"mockHandlersJoined":true,"mockServeJoined":true,"mode":'
for mode in http buffered streaming; do
  accepted='\[true,true,true\]'
  polls=4
  if [[ $mode == http ]]; then accepted='\[false,false,false\]'; polls=0; fi
  expression="^${prefix}\"${mode}\",\"runtimeAccepted\":${accepted},\"runtimePolls\":${polls},\"startIdentityAvailable\":true\\}$"
  check_count 1 "$expression" "$proof/receipts.jsonl"
done
sed -E 's/^.*"childLinuxStartTicks":"([0-9]+)","childPID":([0-9]+).*$/\2:\1/' "$proof/receipts.jsonl" | sort -u > "$proof/child-identities.txt"
[[ $(wc -l < "$proof/child-identities.txt") -eq 3 ]] || fail 'three fresh observed child PID/start identities required'
printf 'status=passed-actual-invocation-executable-fixture-only\ntop_level_tests=1\ntransport_subcases=3\npaired_response_after_observations=9\nphysical_cleanup_receipts=3\n' > "$proof/verification.txt"
