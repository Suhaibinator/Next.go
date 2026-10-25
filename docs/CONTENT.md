# Bounded Markdown content

The portable `content` package parses an explicit Markdown subset into `ui.Node`
using only the standard library and existing UI helpers. It can run in native Go
or Go/WASM; it does not load files, fetch URLs, execute JavaScript, interpret JSX,
compile CSS, interpolate components or evaluate document code. It is an
experimental A11 capability increment, not CommonMark/MDX equivalence or complete
content-pipeline/production parity.

```go
node, err := content.Parse(source, content.Limits{})
if err != nil { return nextgo.Page{}, err }
return nextgo.Page{Body: ui.El("article", nil, node)}, nil
```

`Parse(source string, limits Limits) (ui.Node, error)` validates configuration and
input before returning a complete result. Errors wrap `content.ErrInvalid` for
invalid input/configuration and `content.ErrLimit` for exhausted limits; use
`errors.Is`. Failed parsing returns a nil node. Error text is generic and does
not quote document contents or link credentials. Handle errors before committing
the response. The framework does not choose a fallback or error status for you.

The [standalone example](../examples/content) embeds a local Markdown file into
the native application, renders useful SSR and composes an ordinary trusted Go
component around the result. File selection and publication are application
decisions; the parser itself accepts only an already supplied string. The
existing build-input system tracks declared `go:embed` inputs. No new file
convention, loader discovery, watcher or code generation is introduced here.

## Syntax contract

| Syntax | Supported behavior |
| --- | --- |
| Headings | `# ` through `###### ` at column zero; the remaining trimmed text is inline content. Closing hashes are ordinary text. No generated heading IDs. |
| Paragraphs | Nonblank lines join with one space until a recognized block begins. Blank or whitespace-only lines separate blocks; no hard-break syntax. |
| Unordered lists | Column-zero `- `, `+ ` or `* ` markers form flat consecutive items. Mixed unordered markers share one list. |
| Ordered lists | Column-zero 1–9 ASCII digits followed by `. `; the first number becomes the list's start, including zero. Subsequent numbers do not reset numbering. Blank lines or a different list kind close the list. |
| Quotes | Each consecutive quoted line starts with `>` at column zero; one following space is optional. Repeated `>` produces bounded nested quotes. Blank quote lines still need their `>` prefix. |
| Code fences | Exactly three backticks at column zero, optionally followed by a single 0–32-character ASCII letter/digit/underscore/hyphen language token. Close with a line containing exactly three backticks. Interior lines remain literal, joined by newlines; no final newline is added. The token only sets an escaped `language-*` class; there is no highlighter. |
| Inline code | One backtick opens and the next closes; contents remain literal text. Multi-backtick delimiter semantics are unsupported. |
| Emphasis | `*text*` and `**text**` use the next matching delimiter. Strong text can contain emphasis. This is a simple bounded delimiter grammar, without CommonMark flanking/escape rules. |
| Links | `[label](destination)` uses the first closing `]` and `)`. The label permits code/emphasis but no nested links. Nested/escaped URL parentheses, titles, references and autolinks are unsupported. |

Indentation does not create nested lists or indented code. Images, tables,
task lists, frontmatter, thematic breaks, backslash escapes, entity decoding,
automatic links/IDs and other Markdown dialect features are not implemented.
Unsupported text stays text, while supported delimiters within ordinary text
still follow the rules above. An image prefix prevents the following bracket
from becoming a link; the parser creates no image node or implicit resource load.

Unmatched inline openers and the remainder of that inline segment remain
literal. Unmatched link labels remain literal; a label followed by text instead
of an opening destination parenthesis is also literal. An unclosed code fence
or invalid fence language is an error. CRLF is normalized to LF; a bare CR,
invalid UTF-8, NUL or other Unicode control character except tab/LF is rejected.
There is no best-effort partial document on failure.

## Links and rendering boundaries

Recognized links are validated during parsing. Destinations must be 1–2,048
bytes and use a relative/root-relative URL or fragment, an HTTP(S) URL with a
host, or a nonempty `mailto:`/`tel:` opaque destination. Protocol-relative
authorities, credentials, backslashes, whitespace/control characters and other
schemes are rejected. There is no destination fetch, image optimizer or redirect
validation. Links retain ordinary browser semantics; application base paths are
not automatically prepended. Author mount-aware links or compose them through
the ordinary Go URL helpers outside parsed content.

Every output node uses the existing `ui.Text`, `ui.El` and `ui.Fragment`
serializer. Raw HTML and entity-looking text are escaped, including inside code;
no raw-HTML extension, sanitizer allowlist or trusted-markup bypass is exposed.
The serializer still validates link URLs and escapes attribute values when
rendering. A user-supplied `ui.Node` elsewhere in the application remains trusted
application rendering code; parsing Markdown does not change that boundary.

Compose reusable Go components around parsed nodes using normal functions.
Document text cannot call components, access props/secrets or select imports.
Authorization, whether content is safe to publish, CSP and HTTP cache policy
remain application responsibilities. Escaping does not make confidential text
public-safe, and an allowed external link is not a promise about its destination.

## Resource contract

Zero fields in `Limits` select these defaults; positive overrides must not exceed
the hard ceilings. Negative values and overrides above a ceiling are invalid.

| Limit | Default | Hard ceiling |
| --- | --- | --- |
| `MaxBytes` | 65,536 input bytes | 1,048,576 |
| `MaxLines` | 2,048 | 16,384 |
| `MaxLineBytes` | 8,192 | 65,536 |
| `MaxNodes` | 8,192 | 65,536 |
| `MaxDepth` | 16 | 32 |

Input size is checked before cloning the declared string, preventing retained
substrings from keeping an arbitrarily larger caller allocation alive. Line
bytes are checked after CRLF normalization. A trailing LF creates an additional
empty line and counts toward `MaxLines`. Node accounting includes generated
text, element and fragment nodes. Depth counts recursive quote/link/emphasis
nesting rather than ordinary wrapper tags. Quote/inline recursion is bounded;
delimiter searches consume their spans or stop on an unmatched suffix.

These are per-parse input/work limits, not process concurrency, RSS, response
size or wall-clock guarantees. HTML escaping can expand bytes. Native callers
must bound concurrent parses and responses through their own request policy;
large Go/WASM parsing still occupies browser execution time. Results contain
immutable UI nodes and can be rendered repeatedly/concurrently with independent
writers. There is no cross-request parser state, cache or background work.

Native tests cover supported syntax, malformed input, unsafe destinations,
escaping, exact limits, repeated concurrent rendering and fuzz seeds. Native
and matching-toolchain WASM checks should be rerun for each changed source.
The example's focused native fixture checks useful HTML and versioned navigation;
full generated-app/browser/deployment qualification is separate. All
[capability rows and release gates](PARITY.md) retain their existing scope.

The initial source handoff passes focused Go 1.27.3 native race tests for
`content` and the embedded example, matching-toolchain WASM tests for `content`,
and native/WASM vet. A two-worker five-second fuzz session completed 35,269
executions without a failure; this is short targeted coverage, not a sustained
security review. Its initial default-cache attempt encountered a local sandbox
write denial and was repeated successfully using a task-owned temporary cache.
No generated application build or browser/deployment result is claimed by this
handoff. A11 remains partial and all 11 release gates remain open.
