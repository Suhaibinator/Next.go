# Explicit asynchronous field validation

`client.BindFieldValidation` adds an opt-in browser controller to a field that
already has an ordinary form binding. It separates debounced asynchronous checks
from the existing synchronous `FormOptions.Validate` callback and from mutation
submission. Binding starts no validation or network work. The implemented subset
has scoped frozen-source verification below; it does not close the
[form or release requirements](PARITY.md).

```go
// BindText (or another supported field binding) has already registered "email".
validator, err := client.BindFieldValidation(scope, model, "email",
    func(ctx context.Context, value Profile) (string, error) {
        // Application code supplies the bounded, cancellation-aware check.
        // Empty message + nil error means this check accepted the input.
        // Nonempty message + nil error is a known public field rejection.
        return checkAddress(ctx, value.Email)
    }, client.FieldValidationOptions{
        OnInput: true,
        OnBlur: true,
        Debounce: 300 * time.Millisecond,
        Timeout: 5 * time.Second,
        OnError: func(error) {
            status.Text("The address could not be checked. Retry explicitly.")
        },
    })
if err != nil { return err }
scope.On(retryButton, "click", func(client.Event) {
    if err := validator.Trigger(); err != nil {
        status.Text("The check could not start.")
    }
})
```

`checkAddress` is application code, not a supplied framework endpoint. It must
return promptly after cancellation and validate its own response contract.
Use a size-bounded same-origin request and an intentional redirect policy for
HTTP checks. A known invalid field is a public message with a nil error;
transport failure, malformed responses and validator panics are operational
failures, not proof that a field is valid or that a mutation failed.

The controller belongs to the supplied component scope, exact registered field
and form model. Only one controller can own a model/name pair. `Close()` is
idempotent and is also called by field/model/scope disposal. Replacing a binding
cannot give an older callback authority over a new same-name field.
The validator receives cloned input and its context; it must not call Form,
DOM or controller methods from its worker. DOM/model settlement returns to the
browser event loop. Ordinary form bindings keep their SSR node identity,
pre-WASM adoption, custom validity and native fallback behavior.
Cloning follows `FormOptions.Clone`: its default is only a value copy. Supply
a deep clone for DTOs containing maps, slices, pointers or other mutable
references before handing input to a worker. The form's existing synchronous
clone/equality/validation/merge callbacks remain pure and prompt.

## Triggers, capture and submission

`FieldValidationOptions` contains `OnInput`, `OnBlur`, `Debounce`, `Timeout` and
`OnError`. With both event flags false, the controller is manual-only.
`OnInput` listens to input/change; `OnBlur` requests a check on blur. `Trigger()`
captures ready bound DOM values atomically, records the field as touched and
creates logical pending state immediately, then debounces callback admission.
No input snapshot is retained while waiting. Repeated triggers replace the
logical attempt, even when the text is equal, retaining at most one latest
waiting request per controller.

Zero debounce selects 250 ms and zero timeout selects 10 seconds. Positive
debounce overrides may be at most 5 seconds; timeout overrides may be at most
one minute. Negative durations are invalid. The timeout covers the attempt,
including debounce, composition and waiting behind that controller's preceding
callback; it is not an additional budget added after debounce. Callbacks must
honor cancellation cooperatively.

IME composition keeps the check pending without validating the older model
text. Composition end resumes capture/checking after the field binding adopts
the final text. Eventless autofill is captured again before callback admission.
An actual draft edit retires every pending full-input check, including checks
for other field names, because a validator receives the entire DTO and may
depend on any field. Reset and explicit synchronous validation also retire
older attempts. There is no automatic polling, background revalidation or
retry after an operational failure.

`Form.Begin` rejects a current logical check with `ErrFormValidationPending`,
including its debounce window. Once a check completes with a nonempty current
message, Begin includes that message and returns `ErrFormInvalid`; it does not
silently discard the rejection to submit. The normal submit binding shows
checking status, disables submit buttons and sets `aria-busy` while logical
checks or a submission are pending. Fields remain editable. Programmatic
adapters must handle the same errors themselves. Existing synchronous
`ValidateOnBlur` stays synchronous; it does not become a hidden network hook.

No successful browser check replaces server validation, authorization or a
transaction policy. A native/no-JS submission has no browser validator and must
receive the same application validation on the server. Native constraints and
successful-control rules still apply; custom/native dispatch is not a way to
infer permission from a missing field.

## Result provenance and error sources

Each portable ticket is bound to the full draft revision, reset generation,
validation fence and current field run. A newer same-value trigger, A → B → A
edit, synchronous validation, submission boundary, reset or disposal cannot
restore an old ticket's authority. The browser controller additionally owns its
attempt sequence and exact binding lifetime. A stale/canceled callback may
finish, but cannot publish its message or operational failure into the new view.

Known asynchronous messages have their own error bucket. Synchronous client
errors override asynchronous messages for the same name; known server errors
override both. A valid async result clears only its own async bucket entry and
cannot erase an unchanged synchronous/server error. Explicit synchronous
validation preserves completed async messages while retiring unfinished runs.
Any actual full-draft edit clears async messages, since their input provenance
has changed; named-field synchronous/server clearing keeps its existing rules.
Pure `Form.Validate` does not touch fields or mark submission: its returned/model
errors can therefore coexist with empty untouched DOM alerts. Async Trigger
explicitly touches its target, so applicable async feedback can be displayed.
Messages obey the existing UTF-8, 2,048-byte field and combined 32 KiB/128-name
bounds and are rendered as text/custom validity through ordinary field bindings.

An operational failure settles that attempt without writing a field message.
`OnError` runs only for an applicable operational failure; otherwise the form's
observer-error reporter handles it. An earlier known message can remain until
the input changes, reset, or a fresh successful check supersedes it. Closing a
controller retires work; it does not erase already recorded model errors.
Changing validation meaning or identity policy requires an explicit model/check
transition, not accepting a late result for unchanged text.

`FormSnapshot.Validating` is an owned map of current logical checks, including
waiting checks. `Pending` continues to mean a mutation submission. The separate
`ValidationInFlight` count includes started callbacks that have lost logical
authority but have not physically settled. Do not use those states
interchangeably. Validation does not change the mutation phase, confirmed
baseline or `Unconfirmed` flag. Checking status may temporarily take precedence
in the display without resolving an earlier unknown write outcome.

`Form.Cancel()` now cancels either validation or submission work and returns
whether either was canceled. Canceling only checks preserves the mutation phase
and does not mark an unknown write. Canceling a real submission retains its
existing unknown-outcome semantics. Reset cancels checks and restores the last
confirmed baseline; it does not prove that a server write was undone.

## Admission and cleanup limits

`FormOptions.ValidationLimit` defaults to 8 started tickets per model, with a
maximum of 32. The portable model tracks at most 128 field names until Reset;
waiting tickets retain no DTO. Browser controllers also share one document
ledger across eager and lazy Go runtimes: at most 128 live controllers and
8 admitted validator callbacks. These additive capabilities do not change the
existing module ABI or navigation/action wire versions.

A canceled callback keeps both its started-ticket ownership and document lease
until it actually returns and its posted settlement finishes. Reset, timeout,
unmount, repeated mounts and newer triggers cannot recycle that lease early.
The latest request in the same controller can wait behind its old callback.
Other admission failures retire the waiting attempt and report capacity through
`OnError`; a later explicit trigger/event is required. There is no automatic
global-capacity retry queue.

The ceiling bounds validator callback invocations through return/settlement,
not arbitrary goroutines, allocations or browser promises application code
starts independently. Public Fetch cancellation can return before all underlying
browser work settles; this controller adds no network-settlement primitive.
An uncooperative callback can retain its slot indefinitely. Deadlines cannot
forcibly stop Go code. Choose bounded backend/transport work inside validators.

Close removes owned timers, listeners, observers and logical pending state.
The final callable settlement remains alive while its callback is running;
then it releases ticket, document lease and JS function exactly once. Cleanup
does not publish a late error into a disposed or rebound field. These are
component/document limits, not production load, memory, accessibility or
multi-tab guarantees.

## Portable tickets and custom adapters

The native-testable model exposes the lower-level lifecycle without running
workers or starting timers:

```go
ticket, err := model.BeginFieldValidation(ctx, "email") // logical pending only
if err != nil { return err }
input, err := ticket.Start() // clone/reserve on the model event loop
if err != nil {
    _ = ticket.Cancel() // or retain only a capacity-denied ticket for retry
    return err
}
// Hand only input and ticket.Context() to application work.
// After that work actually returns, marshal settlement to the model event loop:
// applied, err := ticket.Complete(publicMessage)  // known result, including ""
// err := ticket.Fail()                          // operational failure/panic
```

The model and ticket methods remain for one event loop, not concurrent native
callers. Only the immutable context and owned input belong in a worker.
`Cancel()` revokes publication immediately; a started ticket still needs one
`Complete` or `Fail` after callback return. Do not use a timeout watcher to
release a still-running callback's permit. Terminal settlement releases physical
ownership even when output is invalid or the model has closed. Repeated terminal
calls return `ErrFormValidationSettled`; repeated Start never releases an
already-started worker's slot.

Start capacity errors leave a waiting portable ticket retryable; callers must
retry or cancel it explicitly. Other invalid/stale starts retire the ticket.
The pure model starts no timer or watcher and does not schedule settlement for
you. Browser applications should normally use the scoped controller rather
than writing a second timer/callback ownership system.

## Scoped verification and remaining qualification

The [dedicated Go/WASM example](../examples/form-validation) demonstrates two
independent forms, public asynchronous checks, native POST validation and the
additive choice APIs. Its runtime endpoint dependency declares `RequireServer`;
export rejection preserves a previously published static generation.

The frozen source passes full native race/vet on Go 1.27.3 and Go 1.24.13
(60 tested packages and 141 without tests), matching core/mutation/expanded-module
WASM checks and core/module vet on both SDKs. Five application reference checks
also pass. New-fixture browser results are 36 host plus 48 Linux executions,
with the Linux matrix covering Chromium, Firefox, WebKit and touch. Separate
host static tests pass 23 cases; the host primary regression suite separately
passes 335 cases, outside the focused 84-execution form count.
Generated HTTP/buffered-local-Lambda/streaming-
local-Lambda checks pass six cases on each platform at root/mounted asset paths;
the 228-case legacy executable suite passes separately on each platform.
These are platform executions with overlapping scenarios, not an aggregate
distinct-capability or release-conformance total.

Tests cover waiting/debounce submission blocking, known async rejection,
same-value and whole-draft provenance, error precedence, eventless capture,
real composition-listener ordering, reset/rebind/disposal, operational failure,
document-wide eager/module capacity and eventual callable-resource cleanup.
The callback ceiling continues through actual return and posted settlement;
timeout cannot forcibly stop application work or account for unrelated spawned
goroutines/promises.

The browser fixture's initial untouched-alert expectation was corrected to the
existing pure-Validate contract, preserving exact error/no-POST assertions on
submission. The minimum-SDK 15 ms capacity test was replaced with controlled
expiry after all eight real callbacks started; physical-retention assertions
and separate true Go-context-first deadline tests remain. Five focused
minimum-SDK repetitions pass. That repair changes only a `js && wasm` test;
the final production-input witness is unchanged and six rebuilt WASM CODE
sections are byte-identical. It does not establish identical complete artifacts.

Full provenance, the mounted-redirect/teardown corrections and local report
paths are recorded in [FORMS.md](FORMS.md). Its earlier matrices remain
historical and must not be double-counted. Actual AWS, manual accessibility,
physical devices, production overload/recovery, complete form parity and all
11 release gates remain open.
