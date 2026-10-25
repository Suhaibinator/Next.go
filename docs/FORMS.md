# Typed controlled forms

`client.Form[T]` owns a typed Go input, its last confirmed baseline, validation
messages and one active submission. `NewScopedForm` connects it to component
disposal. Browser helpers bind existing inputs without replacing SSR nodes.
There is no virtual DOM, reflection-based field discovery or JavaScript form
logic. The default equality function uses Go `reflect.DeepEqual`; field access
and updates are explicit Go functions.

The `/forms` example enhances an ordinary POST form with Go/WASM. The same
`mutation.Contract` handles JSON and native form submissions. `/field-preview`
uses the model and bindings locally, without an API, and can be statically
exported. `/form-pair` demonstrates independent reusable component instances.

## Model and field bindings

Use a public DTO containing the values needed in the browser:

```go
type Profile struct { Name, Email string; Newsletter bool }

model, err := client.NewScopedForm(scope, initial, client.FormOptions[Profile]{
    Validate: func(value Profile) (client.FieldErrors, error) {
        fields := client.FieldErrors{}
        if strings.TrimSpace(value.Name) == "" { fields["name"] = "Enter a name." }
        return fields, nil
    },
})
if err != nil { return err }

input, err := scope.Require("[data-name]")
if err != nil { return err }
errorNode, err := scope.Require("[data-name-error]")
if err != nil { return err }
err = client.BindText(scope, input, model, "name",
    func(value Profile) string { return value.Name },
    func(value *Profile, text string) { value.Name = text },
    client.FieldBindingOptions{Error: errorNode, ValidateOnBlur: true})
if err != nil { return err }
```

Render the input with `name="name"`, its initial value, and a label on the
server. Give its error region an ID and connect `aria-describedby` in the SSR
markup; a live region such as `role="alert"` announces errors. `BindText`
supports text-like inputs, textareas and single selects of size one.
`BindChecked` binds a checkbox to a Go bool. Render textarea content with an
extra leading newline (`ui.Text("\n" + initial.Bio)`) so the HTML parser's
leading-newline handling preserves the intended initial text. The sample also
normalizes native POST textarea line endings on the server.

Bindings first validate the DOM default against known initial/current/baseline
values. Then they adopt live edits made before WASM loads. Updates assign
`value`/`checked` only when different. They preserve element identity and leave
unchanged input selection alone. IME composition defers model writes and skips
competing value updates until composition ends; an explicit reset ends that
deferment. Input/change, blur and submit stage bound DOM values together before publishing,
so adopting one autofilled field does not overwrite another before reading it.
Only names whose values changed clear their errors; untouched server errors stay.
Composition stays deferred during ordinary field events and blocks submission.
The browser-only `model.Capture()` performs the same atomic capture for custom
adapters before `Begin`; it returns `ErrFormBusy` during composition. Local apply
buttons in both examples call it too. Setters should update only their declared
logical field; use `EditMany` with every affected name for cross-field updates.

Validation runs at `Begin`, before transport. `model.Validate()` also evaluates
the current draft without submitting, touching fields or changing the submission
phase. It returns an owned error map and only publishes when the combined errors
change. Set `FieldBindingOptions.ValidateOnBlur` to opt a control into whole-model
validation after blur records it as touched; the default is false. `/forms?validate=blur`
and `/field-preview` demonstrate this option. It does not filter errors to touched
fields or run asynchronous/debounced validation.
Calling `Validate` can populate `Snapshot.Errors` while the bound alert remains
empty: it does not touch a field or mark the form submitted. Bound error UI
remains gated by touched/submitted state until those transitions occur.

The separate opt-in [`BindFieldValidation` controller](FIELD_VALIDATION.md)
adds explicit debounced asynchronous checks without changing this synchronous
callback contract. Its implemented subset has the scoped qualification below.
It captures a cloned whole-input snapshot, rejects stale results,
separates logical checking from callback admission, and preserves synchronous
and server error precedence. The controller starts no work merely by binding.

Editing clears that field's existing error. Explicit/blur validation preserves
known server errors on unchanged fields, including server-only rules the client
cannot evaluate. Those errors remain until their field changes, reset, or a new
submission attempt. Combined client/server errors obey the same size/count
limits as either source. Bound errors update text, `aria-invalid` and browser
custom validity. Native HTML constraints remain active too. Validation is
application policy and must also run on the server. Reserve adequate error-region
space or position errors so blur feedback cannot move a button between pointer
down and click. The samples reserve two lines for their bounded messages; longer
localized messages need an appropriate layout.

For application updates use `Edit(name, patch)` or atomic `EditMany(names,
patch)`. `Touch(name)` records a visit. `Observe` immediately supplies an owned
snapshot, with later changes delivered synchronously on the Go event loop.
Snapshots contain values, baseline, errors, touched fields, pending/dirty flags,
phase, message, submission/unknown-outcome flags and publication/reset versions.
The additive `Validating` map tracks logical field checks; `ValidationInFlight`
counts started callbacks through settlement. `Pending` remains submission-only.
Observer mutation is coalesced; a runaway notification chain closes the model
after its delivery bound. Observer failures are isolated and reported through
`OnObserverError`.

`NewForm` is also available in native tests without a DOM. Default cloning is a
value copy, suitable for the sample's structs of strings/bools. Supply `Clone`
for mutable maps, slices, pointers or other reference-containing values, and
optionally `Equal`. Inputs, snapshots, edits and confirmed values use that clone
policy. Clone/equality/validation/merge callbacks must be pure, synchronous and
return promptly. The model is for one event loop, not concurrent native callers.

## Radio groups and multiple selects

`BindRadioGroup(scope, controls, model, name, get, set, options...)` binds one
complete native group to a Go string. Pass every radio peer with that name and
form owner, within the same component scope. Values must be unique and nonempty;
`""` in the model means no selection. Native keyboard/focus behavior stays in
the browser. Formless groups use their native tree group; reusable instances
should own separate `<form>` elements so identical names stay independent.

`BindMultiple` binds a `<select multiple>` to `[]string`:

```go
err = client.BindMultiple(scope, topics, model, "topics",
    func(value Preferences) []string { return value.Topics },
    func(value *Preferences, selected []string) { value.Topics = selected },
    client.FieldBindingOptions{Error: topicsError, ValidateOnBlur: true})
```

Supply `Clone` for DTOs containing slices, including a copy of every mutable
slice. The choice example uses `choices.Clone`. Option values must be unique;
selection membership is a set. Matching membership preserves the model's order;
newly adopted choices follow DOM option order. Nil and empty selections match.
Empty option values are supported for multiple selects. Unknown or repeated model
values fail initial binding; invalid later values are reported without changing
the DOM selection. Application validation must still enforce its choice domain.

For an empty-valued radio, use the additive `BindRadioChoice` API with
`client.RadioChoice` rather than the string-only binding:

```go
type Preferences struct {
    Plan client.RadioChoice
    SelectedTopics []int
}

err = client.BindRadioChoice(scope, radios, model, "plan",
    func(value Preferences) client.RadioChoice { return value.Plan },
    func(value *Preferences, choice client.RadioChoice) { value.Plan = choice })
```

`RadioChoice{}` means no selection. `RadioChoice{Value: "", Selected: true}`
means the empty-valued radio is selected. When `Selected` is false, `Value` must
be empty; selected values must belong to the fixed unique option domain. Check
`Selected` when validating presence rather than testing `Value != ""`.
SSR `defaultChecked` and pre-WASM live `checked` state keep the same distinct
meaning. This adds no hidden fields or synthetic DOM value encoding, and leaves
`BindRadioGroup`'s nonempty-value contract unchanged.

For repeated option values, use `BindMultipleOptions` with selected option
positions instead of value strings:

```go
err = client.BindMultipleOptions(scope, topics, model, "topics",
    func(value Preferences) []int { return value.SelectedTopics },
    func(value *Preferences, selected []int) { value.SelectedTopics = selected })
```

Indices are zero-based positions in the native select's complete fixed `options`
collection, including options inside optgroups. They must be unique and in range.
Option values may repeat or be empty; selecting two equal-valued options remains
two selected native nodes. Selection comparison uses membership, so an already
matching model order is retained; newly adopted positions follow DOM order. Nil
and empty index slices select none. Reset uses exact option positions, preserving
which duplicate nodes were selected. Provide a deep `Clone` for every mutable
slice in the DTO. Getter/setter callbacks keep their ordinary pure synchronous
contract. The existing value-based `BindMultiple` still requires unique values.

`client.SelectedOptionValues(values, indices)` converts the selected positions
to an owned value slice in DOM order, preserving repeated/empty values. Its
fixed values list shares the 256-option/256-byte-value bounds. It does not
filter disabled controls, options or optgroups and does not emulate FormData.

Native POST distinguishes an absent radio name from a selected empty value;
native adapters must inspect the field's presence/cardinality instead of relying
only on `url.Values.Get`. Native multiple-select submission preserves repeated
values, but cannot reveal which of two equal-valued option positions supplied
one occurrence. Do not infer index identity from those strings or claim an
identical typed-index roundtrip. Applications choose explicit server contracts
for value multiplicity or a separate fixed identity scheme and validate them.

All choice helpers validate SSR defaults and adopt live pre-WASM selections. They
update existing `checked`/`selected` properties and share the touched/error,
validation, pending/merge, reset and cleanup model. Groups apply ARIA/custom
validity to each radio and use one shared error region. Use labels, a
fieldset/legend and `aria-describedby` in SSR markup.

Radio peers, option identities/order and values are fixed for a binding's
lifetime. Dispose/rebind when changing the schema. Extra peers or changed option
structure cause enhanced submission to retain native POST behavior. Changes to
option labels alone do not require rebinding. Go enhancement sends the typed DTO,
including its disabled-field values; native POST follows browser successful-control
rules (which omit disabled controls/options). Native adapters must intentionally
handle that distinction instead of treating omitted fields as authorization.
Named submitters still retain native behavior.

The `/choices` example validates a public preview using a typed server contract;
it does not persist preferences. Native POST preserves repeated `topics` values
and redirects to fresh SSR with the preview encoded in its URL. `/choice-preview`
and `/choice-pair` use local models without a server action and support static
export. Serialized and URL-encoded choices are public data.

See [file inputs and uploads](FILES.md) for `BindFiles`, opaque browser File
handles, bounded cancellable reads, reset and progressive multipart behavior.

## Progressive POST enhancement

```go
err = client.BindSubmit(scope, formElement, model,
    func(ctx context.Context, input Profile) (client.FormResult[Profile], error) {
        result, err := mutations.Save.Call(ctx, input)
        if err != nil { return client.FormResult[Profile]{}, err }
        return client.FormResult[Profile]{Value: result.Data, Message: "Saved."}, nil
    }, client.SubmitOptions{Status: statusElement})
```

The handler runs in a goroutine, outside the synchronous browser event
callback. It must honor cancellation. Submit helpers validate first, allow only
one pending submission, disable submit buttons while pending and maintain
`aria-busy`. Fields stay editable. Status UI is optional: omit `Status` and use
`Observe` for custom/localized output. Default timeout is 30 seconds; positive
timeouts up to five minutes are supported. `FailureMessage` customizes the
generic ambiguous-failure message.

With an asynchronous field controller, logical checking also disables submit
buttons and sets `aria-busy`; it does not start a mutation. `Begin` returns
`ErrFormValidationPending` while a current check is waiting/running, including
debounce. Current known async errors still reject submission after checking
settles. See [validation timing and outcome semantics](FIELD_VALIDATION.md).

`BindSubmit` requires a POST form owned by the current island and all its named
data controls to have matching bindings. It accepts associated controls within
the owning scope, including controls inside composed child scopes. It preserves
native behavior for named submitters, override attributes, image submitters,
non-self targets, different origins/credentials, and later structural changes
that introduce unbound controls. GET forms retain their native behavior instead
of receiving a POST enhancement. These paths do not silently discard native
submission data. The profile demo's named button deliberately exercises native
POST after hydration.

Render an actual server action URL and useful native controls. A failed binding
leaves native submission available. On binding disposal, owned listeners and
subscriptions are released and custom validity/ARIA state is restored, so Go
errors do not permanently block a later native submission. Do not compete with
bindings for their `value`, `checked`, error or submit-button properties. Dispose
and rebind when replacing controls or changing their schema/names. Supported
control names should not shadow the form's DOM APIs.

Serialized inputs, errors and props are public browser data. A typed contract
does not authenticate a requester. Native handlers still need authorization,
body limits, domain validation and durable application storage. See
[typed mutations](MUTATIONS.md) for transport/origin/redirect/schema behavior.

## Pending edits, confirmation and unknown outcomes

Each submission owns a cloned input snapshot and cancellation context.
`Begin(ctx)`, ticket `Input()`/`Context()`, `Accept`, `Reject`, `Fail` and `Cancel`
are also available when building a custom adapter. A direct caller must settle
or cancel its ticket; the pure model does not start a timeout watcher.

A successful result updates the confirmed baseline. If values changed after
submission, the default policy preserves the entire newer input. Optional
`Merge(current, submitted, confirmed)` can accept canonical values for unchanged
fields while preserving newer edits. The demo does this for user fields and
always accepts the server's record version. Without such a policy the framework
cannot infer which DTO fields are user edits versus server bookkeeping.
Late server field errors are discarded when edits occurred after the submitted
snapshot. A newer draft's client validation errors survive an older rejection or
transport failure. Accepting an older successful response revalidates the retained
or merged draft, so a newly invalid edit remains visible while the confirmed
baseline/version advances. This does not turn that older successful request into
a rejection. Only one ticket can settle; late/canceled/unmounted results cannot
replace the current view.

`FormError` from a handler denotes a known application rejection with public
field messages. Other errors and handler panics use the generic failure message
and mark `Unconfirmed`. Use `Reject` only for a known rejection; use `Fail` for
transport, protocol or other ambiguous outcomes. The profile adapter only maps
the endpoint's validation response to `FormError`.

Canceling or resetting a pending submission aborts owned transport but cannot
prove that a server write was undone. `Unconfirmed` survives reset and later
rejections; a confirmed success or a newly loaded authoritative model resolves
it. No automatic retry, transaction, rollback or idempotency guarantee is
provided. Reconcile application/server state before retrying an unknown result.
The sample's version comparison rejects stale writes; it is not a general
distributed transaction or idempotency protocol.

`Reset` restores the last confirmed baseline, clears field errors/touched state
and cancels pending work. A baseline is the last confirmed value, not a promise
that no other writer has changed the server. `Close` cancels active work,
unregisters bindings and drops owned values/callbacks. Repeated close is harmless.
Parent disposal applies the [component lifetime](COMPONENT_LIFETIMES.md) rules.
`Cancel()` returns true for canceled validation or submission work; validation-only
cancellation preserves the mutation phase and does not set `Unconfirmed`.

## Limits, demonstrations and remaining work

Framework-owned limits: 128 bound fields, 256 bound/associated controls, 64 radios
per group, 256 options per multiple select, 256-byte choice values, 256 observers,
128 touched/error names, 128-byte field names, 2,048-byte field messages,
32 KiB total field-error data and 4,096-byte form messages. Publication drains at
most 4,096 observer deliveries before closing a runaway model. These do not
bound arbitrary DTOs, cloning/callback work, application allocations or
goroutines. Typed mutation transport has its own byte limits.

The profile repository is public process-local demo data, capped at 256 records.
Its `role` field is demonstration data, not an authorization capability. It
resets on restart and cannot provide consistent storage across independent
serverless instances. Use authenticated handlers and durable shared storage in
a deployed application. `/forms` requires a server; `/field-preview` performs
local previews and works on a plain static host. Local apply/reset buttons are
disabled until Go loads.

The additive empty-valued-radio and duplicate-valued-option controllers are
implemented with the scoped current-source qualification below. The separate
async field-validation controller and portable tickets have their own
[limits and provenance contract](FIELD_VALIDATION.md).
Complete disabled/native submission equivalence, streaming/resumable upload
controls and progress, dynamic
choice/file schemas and field arrays,
automatic reactive/background validation policies, optimistic-ledger integration, richer native
validation-page reconstruction and complete form/browser/accessibility/cloud
qualification remain open. Named submitter dispatch remains native. These are
unfinished capabilities in the [full inventory](PARITY.md), not removed from the
framework's production-parity objective.

The initial qualified subset passes 108 runtime checks (27 Chromium and 81
Linux Firefox/WebKit/touch, including existing component-lifetime regressions)
and 84 plain-static-host checks (21 host and 63 Linux). Both supported Go SDKs
pass native race/vet and matching core/module WASM checks, with typed-route
generation drift checks. Full standalone HTTP and buffered/streaming mock Lambda
suites pass on the host and under nonroot/read-only Linux. The new Lambda
fixture initially used an HTTP origin for the adapter's HTTPS default; its
correction preserves the strict origin check. A static-host rejection assertion
was interrupted by that source edit; its complete host suite passed after
sources were frozen. The repeated-navigation listener test now waits for the
committed destination and avoids duplicate test-link IDs; its listener checks
remain strict. These reports qualify the implemented subset, not a release.

The explicit/blur-validation extension passes **120 runtime and 84 static checks**
(30 Chromium runtime, 90 Linux Firefox/WebKit/touch runtime, 21 host static and
63 Linux static). These frozen-source runs include validation before submission,
unchanged server-only errors, a newer invalid draft surviving an older accepted
save, default submit-only behavior, and static local validation. Native model
tests additionally check atomic callback failure, closed/reentrant ownership,
owned results, combined client/server error bounds, and newer draft errors after
rejection/transport failure. Both SDKs pass native race/vet, matching core/module
WASM tests/vet and route-generation checks. Standalone HTTP and both mock Lambda
suites pass again on the host and in nonroot/read-only Linux. The first native
launch was blocked from opening loopback listeners by the sandbox; the corrected
run permits them. The first Linux project selector matched no projects; the
corrected run names all six projects explicitly. Neither required an application
change or weaker assertion. The broader production qualification remains open.

The radio/multiple-select and atomic-capture extension passes **176 runtime and
88 static-host checks**: 44/22 on host Chromium and 132/66 across Linux
Firefox/WebKit/touch. The frozen-source matrix covers existing form/composition
regressions as well as radio/multiple SSR identity, pre-WASM selection, native
keyboard behavior, formless groups, independent form owners, repeated native
fields, no-JS submission, pending edits, changed-option fallback, invalid-draft
reset, focused-control/sibling autofill and listener cleanup. Both SDKs pass
native race/vet, matching core/module WASM tests/vet and generated-route drift
checks. HTTP and both mock Lambda suites pass on host and nonroot/read-only Linux;
the form executable fixture now includes eleven steps with choice POST/SSR flows.

Qualification found two application/runtime issues before that passing matrix.
Blur could publish old values over focused eventless autofill; field events now
capture ready bindings atomically and only clear errors on changed names. An
error appearing during pointer activation could move the local Reset button;
the examples now reserve error space, and the original first-click assertion
remains. Native/WASM tests also cover capture atomicity, composition admission,
panics/reentrancy and untouched server errors. Initial focus setup and a dev
reload during a formless-group test were corrected before production runs.

An earlier host attempt saw a renderer crash during initial hydration and a
static browser-context setup failure. Their root cause is unproven. A subsequent
environment interruption left no running preview/test processes or temporary
logs. Fresh sequential host suites with browser diagnostics, followed by the
Linux/SDK/deployment matrix above, passed without weaker assertions. Recovery
logs are kept under `.nextgo-test-results/qualification/choices-recovery/`.
These are working-tree qualification results, not an immutable production release
or proof of the remaining browser, accessibility, performance and cloud gates.

The newer [file-control extension](FILES.md) passes 212 runtime and 92 static-host
checks (304 total) across the same four browser configurations, including the
existing form/composition regressions. Both SDKs, matching WASM/vet, generation
and host/Linux HTTP/both mock Lambda suites pass. Browser File handles, bounded
abortable reads and native multipart work; multipart browser transport, streaming,
resumable/direct-storage protocols and broader file qualification remain open.

The [native multipart extension](MULTIPART.md) passes 292 runtime and 92 static
checks, expanding coverage to typed-mutation protocol and optimistic-state
regressions alongside the form/composition subset. Both SDK and deployment
matrices pass. Files can now be sent through FormData without Go byte/base64
copies; streaming/resumable protocols and progress remain open.

Current bundle cost is recorded in [PERFORMANCE.md](PERFORMANCE.md). The eager
sample still exceeds its proposed raw/gzip size budgets.

## Additive choice and async-validation qualification

The preceding matrices describe their historical subsets; they are not added
together with this newer run. The frozen new source passes full native race/vet
on Go **1.27.3** and minimum Go **1.24.13**, covering **60 tested packages** and
**141 packages without tests**. Matching-toolchain WASM checks pass for core,
mutation and the expanded module-target selection on both SDKs, with core/module
vet and generated-reference checks for five applications.

The new browser fixture passes **36 host** and **48 Linux** cases (**84 platform
executions**, not 84 distinct scenarios). Linux covers Chromium, Firefox,
WebKit and WebKit touch. Host static checks separately pass **23** cases.
The current host primary regression suite separately passes **335** cases;
those regressions are not included in the focused 84-case form count.
The generated form application passes **six transport cases per platform**
(host and non-root/read-only Linux): native HTTP, buffered local Lambda and
streaming local Lambda, each at root and a mounted asset path. These are
**12 platform transport executions**, with repeated semantics across platforms.
The existing **228-case** executable HTTP/mock-Lambda regression suite also
passes independently on each platform; it is not part of the new-fixture count.

Coverage includes selected-empty versus absent radios, duplicate option identity
and native value multiplicity, independent forms, delayed and failed checks,
same-value/edited/reset result fencing, validation-before-submit, composition,
eager/module admission, cleanup, useful no-JS POST and retained static output
after `RequireServer` export refusal. Known current async invalidity blocks
submission; canceling checks alone does not turn them into unknown writes.
Admission bounds validator callback invocation through posted settlement, not
arbitrary work or browser promises application code starts independently.

Qualification retained and corrected fixture mistakes without changing those
contracts. An initial browser run passed 44 cases and failed four because it
expected untouched alerts immediately after pure `Form.Validate`; the corrected
assertion checks invalid model state, empty untouched alerts and zero HTTP work,
then exact visible errors after the real Go submit attempt. A mounted raw HTTP
fixture now asserts its canonical 308 redirect. A Playwright teardown orphan
was addressed with graceful SIGTERM configuration and final cleanup checks.
These were fixture corrections, not runtime behavior changes.

The minimum-SDK capacity test originally let a real 15 ms deadline expire before
eight callbacks had started. Its `js && wasm` test now establishes an eight-worker
start barrier and controlled expiry, retaining the same physical-admission
assertions; five minimum-SDK repetitions pass. Separate true Go-context-before-
JS-timer deadline regressions remain. Only that test source changed: production
runtime inputs stayed unchanged, and CODE sections in all six rebuilt
demo/form/workspace WASM bundles remain byte-identical. This does not mean whole
artifact bytes or build IDs are identical.

Local records are retained at
`.nextgo-test-results/qualification/form-validation-final/qualification-final.json`
for Linux and `.nextgo-test-results/qualification/form-validation/final-host/`
for host fixture output. The integrated 884-input runtime witness remains
unchanged for the final production source. These scoped local results do not
qualify actual AWS, complete accessibility/device/load/recovery behavior, all
form capabilities or production maturity. All 79 capabilities and 11 open
release gates remain tracked.
