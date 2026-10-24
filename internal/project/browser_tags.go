package project

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const moduleBuildTag = "nextgo_module"

// Query Go, rather than only os.Getenv: GOFLAGS may come from `go env -w`.
// Other flags stay in Go's environment; only -tags is merged explicitly.
func browserBuildTags(ctx context.Context, root string) (core, module string, err error) {
	data, err := goInputJSON(ctx, root, "env", "-json", "GOFLAGS")
	if err != nil {
		return "", "", err
	}
	var environment struct{ GOFLAGS string }
	if err := json.Unmarshal(data, &environment); err != nil {
		return "", "", fmt.Errorf("read Go build flags: %w", err)
	}
	tags, err := applicationBuildTags(environment.GOFLAGS)
	if err != nil {
		return "", "", err
	}
	core, err = buildTagsFlag(tags)
	if err != nil {
		return "", "", err
	}
	module, err = buildTagsFlag(append(tags, moduleBuildTag))
	return core, module, err
}

// Force Go's legacy word-list form with a trailing space. Joining commas would
// incorrectly turn a literal comma inside a legacy tag into two enabled tags.
func buildTagsFlag(tags []string) (string, error) {
	words := make([]string, len(tags))
	for i, tag := range tags {
		switch {
		case tag != "" && !strings.ContainsAny(tag, " \t\r\n") && tag[0] != '\'' && tag[0] != '"':
			words[i] = tag
		case !strings.ContainsRune(tag, '\''):
			words[i] = "'" + tag + "'"
		case !strings.ContainsRune(tag, '"'):
			words[i] = "\"" + tag + "\""
		default:
			return "", fmt.Errorf("build-tag word cannot be represented in Go flag syntax")
		}
	}
	return "-tags=" + strings.Join(words, " ") + " ", nil
}

func applicationBuildTags(flags string) ([]string, error) {
	words, err := goFlagWords(flags)
	if err != nil {
		return nil, fmt.Errorf("invalid GOFLAGS quoting")
	}
	var tags []string
	for _, word := range words {
		flag := strings.TrimPrefix(word, "-")
		flag = strings.TrimPrefix(flag, "-")
		if flag == "tags" {
			return nil, fmt.Errorf("GOFLAGS -tags requires a value in the same flag")
		}
		value, found := strings.CutPrefix(flag, "tags=")
		if !found {
			continue
		}
		// Match the Go command's legacy space/quoted form and modern comma form.
		if strings.ContainsAny(value, " '") {
			tags, err = goFlagWords(value)
			if err != nil {
				return nil, fmt.Errorf("invalid GOFLAGS build-tag quoting")
			}
		} else {
			tags = nil
			for _, tag := range strings.Split(value, ",") {
				if tag != "" {
					tags = append(tags, tag)
				}
			}
		}
	}
	for _, tag := range tags {
		if tag == moduleBuildTag {
			return nil, fmt.Errorf("build tag nextgo_module is reserved for generated component executables; remove it from GOFLAGS")
		}
	}
	return tags, nil
}

// Go's GOFLAGS syntax quotes whole words, with no shell expansion or escape
// processing. Quotes inside an unquoted word are literal. Keep this parser
// deliberately smaller than a shell; it never executes or logs flag values.
func goFlagWords(input string) ([]string, error) {
	var words []string
	for input != "" {
		input = strings.TrimLeft(input, " \t\r\n")
		if input == "" {
			break
		}
		if input[0] == '\'' || input[0] == '"' {
			quote := input[0]
			end := strings.IndexByte(input[1:], quote)
			if end < 0 {
				return nil, fmt.Errorf("unclosed quoted word")
			}
			words = append(words, input[1:end+1])
			input = input[end+2:]
			continue
		}
		end := strings.IndexAny(input, " \t\r\n")
		if end < 0 {
			words = append(words, input)
			break
		}
		words = append(words, input[:end])
		input = input[end:]
	}
	return words, nil
}
