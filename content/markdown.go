// Package content parses an explicitly limited Markdown subset into escaped UI
// nodes. It does not execute content, resolve files, fetch URLs or accept HTML.
package content

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/ui"
)

var (
	ErrInvalid = errors.New("content: invalid input or configuration")
	ErrLimit   = errors.New("content: limit exceeded")
)

// Limits bounds input and parser work. Zero fields select defaults. MaxDepth
// counts recursive quote/link/emphasis nesting, rather than generated HTML tags.
// MaxNodes counts text, element and fragment nodes, including empty text nodes.
type Limits struct {
	MaxBytes, MaxLines, MaxLineBytes, MaxNodes, MaxDepth int
}

func (l Limits) normalized() (Limits, error) {
	values := []*int{&l.MaxBytes, &l.MaxLines, &l.MaxLineBytes, &l.MaxNodes, &l.MaxDepth}
	defaults := []int{64 << 10, 2048, 8 << 10, 8192, 16}
	ceilings := []int{1 << 20, 16384, 64 << 10, 65536, 32}
	for i, value := range values {
		if *value == 0 {
			*value = defaults[i]
		}
		if *value < 1 || *value > ceilings[i] {
			return l, fmt.Errorf("%w: limits must be positive and within hard ceilings", ErrInvalid)
		}
	}
	return l, nil
}

type parser struct {
	limits Limits
	nodes  int
	err    error
}

func (p *parser) node(node ui.Node) ui.Node {
	p.nodes++
	if p.nodes > p.limits.MaxNodes && p.err == nil {
		p.err = fmt.Errorf("%w: nodes", ErrLimit)
	}
	return node
}
func (p *parser) text(value string) ui.Node { return p.node(ui.Text(value)) }
func (p *parser) element(tag string, attrs ui.Attrs, children ...ui.Node) ui.Node {
	return p.node(ui.El(tag, attrs, children...))
}
func (p *parser) depth(depth int) bool {
	if depth > p.limits.MaxDepth && p.err == nil {
		p.err = fmt.Errorf("%w: nesting", ErrLimit)
	}
	return p.err == nil
}

// Parse accepts ATX headings, paragraphs, flat lists, blockquotes, triple-backtick
// fences, inline backticks, star emphasis/strong and explicit links. Raw HTML is
// ordinary escaped text. Unmatched inline delimiters are literal; an unclosed
// fence, invalid UTF-8/control character or unsafe recognized link is an error.
// No partial result is returned on error. Parsed nodes retain no mutable input.
func Parse(source string, limits Limits) (ui.Node, error) {
	l, err := limits.normalized()
	if err != nil {
		return nil, err
	}
	if len(source) > l.MaxBytes {
		return nil, fmt.Errorf("%w: input bytes", ErrLimit)
	}
	// Substrings stored by UI nodes must not retain a caller's larger backing
	// allocation. Clone only after the declared input size has been bounded.
	source = strings.Clone(source)
	if !utf8.ValidString(source) {
		return nil, fmt.Errorf("%w: UTF-8", ErrInvalid)
	}
	source = strings.ReplaceAll(source, "\r\n", "\n")
	for _, r := range source {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return nil, fmt.Errorf("%w: control character", ErrInvalid)
		}
	}
	if strings.Count(source, "\n") >= l.MaxLines {
		return nil, fmt.Errorf("%w: lines", ErrLimit)
	}
	lines := strings.Split(source, "\n")
	for _, line := range lines {
		if len(line) > l.MaxLineBytes {
			return nil, fmt.Errorf("%w: line bytes", ErrLimit)
		}
	}
	p := &parser{limits: l}
	result := p.blocks(lines, 0)
	if p.err != nil {
		return nil, p.err
	}
	return result, nil
}

func heading(line string) (int, string) {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n >= 1 && n <= 6 && n < len(line) && line[n] == ' ' {
		return n, strings.TrimSpace(line[n+1:])
	}
	return 0, ""
}

func listItem(line string) (tag, value, start string) {
	if len(line) >= 2 && strings.ContainsRune("-*+", rune(line[0])) && line[1] == ' ' {
		return "ul", line[2:], ""
	}
	n := 0
	for n < len(line) && line[n] >= '0' && line[n] <= '9' {
		n++
	}
	if n > 0 && n <= 9 && len(line) > n+1 && line[n:n+2] == ". " {
		number, _ := strconv.Atoi(line[:n])
		return "ol", line[n+2:], strconv.Itoa(number)
	}
	return "", "", ""
}

func special(line string) bool {
	h, _ := heading(line)
	list, _, _ := listItem(line)
	return strings.TrimSpace(line) == "" || h != 0 || list != "" || strings.HasPrefix(line, ">") || strings.HasPrefix(line, "```")
}

func (p *parser) blocks(lines []string, depth int) ui.Node {
	if !p.depth(depth) {
		return nil
	}
	var nodes []ui.Node
	for i := 0; i < len(lines) && p.err == nil; {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			i++
			continue
		}
		if strings.HasPrefix(line, "```") {
			language := strings.TrimSpace(line[3:])
			if len(language) > 32 {
				p.err = fmt.Errorf("%w: fence language", ErrInvalid)
				break
			}
			for _, r := range language {
				if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
					p.err = fmt.Errorf("%w: fence language", ErrInvalid)
					break
				}
			}
			end := i + 1
			for end < len(lines) && lines[end] != "```" {
				end++
			}
			if end == len(lines) {
				p.err = fmt.Errorf("%w: unclosed code fence", ErrInvalid)
				break
			}
			var attrs ui.Attrs
			if language != "" {
				attrs = ui.Attrs{"class": "language-" + language}
			}
			code := p.element("code", attrs, p.text(strings.Join(lines[i+1:end], "\n")))
			nodes = append(nodes, p.element("pre", nil, code))
			i = end + 1
			continue
		}
		if strings.HasPrefix(line, ">") {
			var quoted []string
			for i < len(lines) && strings.HasPrefix(lines[i], ">") {
				quoted = append(quoted, strings.TrimPrefix(strings.TrimPrefix(lines[i], ">"), " "))
				i++
			}
			nodes = append(nodes, p.element("blockquote", nil, p.blocks(quoted, depth+1)))
			continue
		}
		if level, text := heading(line); level != 0 {
			nodes = append(nodes, p.element("h"+strconv.Itoa(level), nil, p.inline(text, depth, true)...))
			i++
			continue
		}
		if tag, _, start := listItem(line); tag != "" {
			var items []ui.Node
			for i < len(lines) {
				kind, text, _ := listItem(lines[i])
				if kind != tag || p.err != nil {
					break
				}
				items = append(items, p.element("li", nil, p.inline(text, depth, true)...))
				i++
			}
			var attrs ui.Attrs
			if tag == "ol" && start != "1" {
				attrs = ui.Attrs{"start": start}
			}
			nodes = append(nodes, p.element(tag, attrs, items...))
			continue
		}
		end := i + 1
		for end < len(lines) && !special(lines[end]) {
			end++
		}
		nodes = append(nodes, p.element("p", nil, p.inline(strings.Join(lines[i:end], " "), depth, true)...))
		i = end
	}
	return p.node(ui.Fragment(nodes...))
}

func linkURL(value string) error {
	if value == "" || len(value) > 2048 || strings.HasPrefix(value, "//") || strings.Contains(value, "\\") {
		return fmt.Errorf("%w: link destination", ErrInvalid)
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("%w: link whitespace/control", ErrInvalid)
		}
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil {
		return fmt.Errorf("%w: link URL", ErrInvalid)
	}
	switch strings.ToLower(u.Scheme) {
	case "":
		if u.Host == "" {
			return nil
		}
	case "http", "https":
		if u.Hostname() != "" && u.Opaque == "" {
			return nil
		}
	case "mailto", "tel":
		if u.Opaque != "" && u.Host == "" {
			return nil
		}
	}
	return fmt.Errorf("%w: link scheme or authority", ErrInvalid)
}

func (p *parser) inline(text string, depth int, links bool) []ui.Node {
	if !p.depth(depth) {
		return nil
	}
	var nodes []ui.Node
	plain := 0
	flush := func(end int) {
		if end > plain {
			nodes = append(nodes, p.text(text[plain:end]))
		}
	}
	for i := 0; i < len(text) && p.err == nil; {
		if text[i] == '`' || text[i] == '*' {
			marker, tag := text[i:i+1], "em"
			if marker == "`" {
				tag = "code"
			} else if strings.HasPrefix(text[i:], "**") {
				marker, tag = "**", "strong"
			}
			end := strings.Index(text[i+len(marker):], marker)
			if end < 0 {
				break // An unmatched opener and its remainder stay literal.
			}
			end += i + len(marker)
			flush(i)
			value := text[i+len(marker) : end]
			var children []ui.Node
			if tag == "code" {
				children = []ui.Node{p.text(value)}
			} else {
				children = p.inline(value, depth+1, links)
			}
			nodes = append(nodes, p.element(tag, nil, children...))
			i, plain = end+len(marker), end+len(marker)
			continue
		}
		if links && text[i] == '[' && (i == 0 || text[i-1] != '!') {
			labelEnd := strings.IndexByte(text[i+1:], ']')
			if labelEnd < 0 {
				break
			}
			labelEnd += i + 1
			if labelEnd+1 < len(text) && text[labelEnd+1] == '(' {
				end := strings.IndexByte(text[labelEnd+2:], ')')
				if end < 0 {
					break
				}
				end += labelEnd + 2
				value := text[labelEnd+2 : end]
				if err := linkURL(value); err != nil {
					p.err = err
					break
				}
				flush(i)
				nodes = append(nodes, p.element("a", ui.Attrs{"href": value}, p.inline(text[i+1:labelEnd], depth+1, false)...))
				i, plain = end+1, end+1
				continue
			}
			// Consume an unmatched label together to avoid repeated suffix scans.
			i = labelEnd + 1
			continue
		}
		i++
	}
	flush(len(text))
	return nodes
}
