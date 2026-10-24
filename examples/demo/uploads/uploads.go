package uploads

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

const MaxFileBytes = 256 << 10
const MaxFiles = 2

type Content struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}
type Input struct {
	Title string    `json:"title"`
	Files []Content `json:"files"`
}
type ReceivedFile struct {
	Name   string `json:"name"`
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
}
type Receipt struct {
	ID    string         `json:"id"`
	Title string         `json:"title"`
	Files []ReceivedFile `json:"files"`
}

func ValidTitle(title string) bool {
	return len(strings.TrimSpace(title)) > 0 && len(title) <= 80 && utf8.ValidString(title) && strings.IndexFunc(title, func(r rune) bool { return r < 32 || r == 127 }) < 0
}
func ValidName(name string) bool {
	return name != "" && len(name) <= 255 && utf8.ValidString(name) && !strings.ContainsAny(name, "/\\") && strings.IndexFunc(name, func(r rune) bool { return r < 32 || r == 127 }) < 0
}
func Validate(input Input) map[string]string {
	fields := map[string]string{}
	if !ValidTitle(input.Title) {
		fields["title"] = "Enter a label from 1 to 80 bytes."
	}
	if len(input.Files) < 1 || len(input.Files) > MaxFiles {
		fields["files"] = "Choose one or two files."
		return fields
	}
	for _, file := range input.Files {
		if !ValidName(file.Name) || len(file.Data) > MaxFileBytes {
			fields["files"] = "Use valid file names and at most 256 KiB per file."
		}
	}
	return fields
}
func Compute(ctx context.Context, input Input) (Receipt, error) {
	if len(Validate(input)) > 0 {
		return Receipt{}, fmt.Errorf("invalid upload input")
	}
	receipt := Receipt{Title: strings.TrimSpace(input.Title), Files: []ReceivedFile{}}
	for _, file := range input.Files {
		if err := ctx.Err(); err != nil {
			return Receipt{}, err
		}
		digest := sha256.Sum256(file.Data)
		receipt.Files = append(receipt.Files, ReceivedFile{Name: file.Name, Size: len(file.Data), SHA256: hex.EncodeToString(digest[:])})
	}
	return receipt, ctx.Err()
}
func CloneReceipt(receipt Receipt) Receipt {
	receipt.Files = append([]ReceivedFile(nil), receipt.Files...)
	return receipt
}
func Summary(receipt Receipt) string {
	rows := []string{}
	for _, file := range receipt.Files {
		rows = append(rows, fmt.Sprintf("%s (%d bytes): %s", file.Name, file.Size, file.SHA256))
	}
	return strings.Join(rows, "\n")
}
