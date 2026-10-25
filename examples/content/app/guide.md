# A small, explicit content format

This page starts as **real HTML**. Its content is embedded in the native Go program and parsed into the same escaped UI nodes as handwritten components.

## A bounded syntax subset

- Headings, paragraphs and flat lists
- *Emphasis*, **strong text**, and `inline code`
- Explicit links, such as [back to this guide](#guide)

> Content stays data.
> There is no JavaScript, JSX, filesystem access or component execution.

```go
node, err := content.Parse(source, content.Limits{})
if err != nil {
    return nextgo.Page{}, err
}
```

<script>This raw markup is displayed as text, never executed.</script>

1. Choose and bound the input.
2. Handle parsing errors before returning the page.
3. Compose trusted Go components around the result.
