package routing

import (
	"net/url"
	"strings"
)

// Index owns an immutable, validated route set. Lookup follows Less specificity
// regardless of input order, while returned indices refer to the input slice.
// It can be shared by concurrent requests without locks or request-time writes.
type Index struct {
	root     *indexNode
	static   map[string]int
	patterns [][]segment
}

type indexNode struct {
	literals                  map[string]*indexNode
	parameter                 *indexNode
	exact, required, optional int
}

func newIndexNode() *indexNode { return &indexNode{exact: -1, required: -1, optional: -1} }

func NewIndex(patterns []string) (*Index, error) {
	if err := ValidatePatterns(patterns); err != nil {
		return nil, err
	}
	index := &Index{root: newIndexNode(), static: make(map[string]int), patterns: make([][]segment, len(patterns))}
	for i, pattern := range patterns {
		parts, _, _ := parse(pattern)
		index.patterns[i] = parts
		node := index.root
		literal := true
		for _, part := range parts {
			switch part.kind {
			case static:
				if node.literals == nil {
					node.literals = make(map[string]*indexNode)
				}
				if node.literals[part.value] == nil {
					node.literals[part.value] = newIndexNode()
				}
				node = node.literals[part.value]
			case parameter:
				literal = false
				if node.parameter == nil {
					node.parameter = newIndexNode()
				}
				node = node.parameter
			case catchall:
				literal = false
				node.required = i
			case optionalCatchall:
				literal = false
				node.optional = i
			}
		}
		if len(parts) == 0 || parts[len(parts)-1].kind < catchall {
			node.exact = i
		}
		if literal {
			index.static[pattern] = i
		}
	}
	return index, nil
}

// Lookup selects a route without allocating a parameter map. Static ASCII and
// Unicode paths without percent escapes use the direct literal lookup.
func (index *Index) Lookup(escapedPath string) (int, bool) {
	at, _, ok := index.lookup(escapedPath)
	return at, ok
}

// Match decodes each request segment once and allocates parameters only for
// the selected route. Path admission and catch-all values match Match exactly.
func (index *Index) Match(escapedPath string) (int, map[string]string, bool) {
	at, parts, ok := index.lookup(escapedPath)
	if !ok {
		return 0, nil, false
	}
	params := map[string]string{}
	for position, part := range index.patterns[at] {
		switch part.kind {
		case parameter:
			params[part.value] = parts[position]
		case catchall, optionalCatchall:
			params[part.value] = strings.Join(parts[position:], "/")
		}
	}
	return at, params, true
}

func (index *Index) lookup(escapedPath string) (int, []string, bool) {
	if index == nil || index.root == nil || !strings.HasPrefix(escapedPath, "/") || strings.ContainsAny(escapedPath, "?#") {
		return 0, nil, false
	}
	if !strings.Contains(escapedPath, "%") {
		if at, ok := index.static[escapedPath]; ok {
			return at, nil, true
		}
	}
	var parts []string
	if escapedPath != "/" {
		parts = strings.Split(escapedPath[1:], "/")
		for i, part := range parts {
			value, err := url.PathUnescape(part)
			if err != nil || !safeSegment(value) {
				return 0, nil, false
			}
			parts[i] = value
		}
	}
	// Use an explicit DFS stack: untrusted path depth must not grow the Go call
	// stack. Each trie node is visited at most once, even for overlapping paths.
	type frame struct {
		node            *indexNode
		position, route int
	}
	var local [16]frame
	stack := append(local[:0], frame{node: index.root})
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		if current.node == nil {
			return current.route, parts, true
		}
		node, position := current.node, current.position
		if position == len(parts) {
			if node.exact >= 0 {
				return node.exact, parts, true
			}
			if node.optional >= 0 {
				return node.optional, parts, true
			}
			continue
		}
		// Reverse insertion gives static > parameter > required > optional.
		if node.optional >= 0 {
			stack = append(stack, frame{route: node.optional})
		}
		if node.required >= 0 {
			stack = append(stack, frame{route: node.required})
		}
		if node.parameter != nil {
			stack = append(stack, frame{node: node.parameter, position: position + 1})
		}
		if child := node.literals[parts[position]]; child != nil {
			stack = append(stack, frame{node: child, position: position + 1})
		}
	}
	return 0, nil, false
}
