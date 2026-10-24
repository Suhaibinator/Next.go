package profiles

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

type Profile struct {
	ID         string `json:"id"`
	Version    string `json:"version"`
	Name       string `json:"name"`
	Bio        string `json:"bio"`
	Email      string `json:"email"`
	Role       string `json:"role"`
	Newsletter bool   `json:"newsletter"`
}

func ValidID(id string) bool {
	if len(id) < 1 || len(id) > 40 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
func Validate(p Profile) map[string]string {
	fields := map[string]string{}
	if !ValidID(p.ID) {
		fields["id"] = "Choose a valid public demo record ID."
	}
	if _, err := strconv.ParseUint(p.Version, 10, 64); err != nil {
		fields["version"] = "Reload the profile before saving."
	}
	if name := strings.TrimSpace(p.Name); name == "" || len(name) > 80 || !utf8.ValidString(name) || strings.IndexFunc(name, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		fields["name"] = "Enter a name from 1 to 80 bytes."
	}
	if len(p.Bio) > 512 || !utf8.ValidString(p.Bio) || strings.ContainsRune(p.Bio, 0) {
		fields["bio"] = "Keep the notes within 512 UTF-8 bytes."
	}
	email := strings.TrimSpace(p.Email)
	if !utf8.ValidString(email) || len(email) > 160 || strings.Count(email, "@") != 1 || strings.HasPrefix(email, "@") || strings.HasSuffix(email, "@") || strings.IndexFunc(email, func(r rune) bool { return r <= 32 || r == 127 }) >= 0 {
		fields["email"] = "Enter an email address."
	}
	if p.Role != "reader" && p.Role != "editor" {
		fields["role"] = "Choose reader or editor."
	}
	return fields
}

// Merge preserves fields changed after submission while accepting server version
// and normalization for unchanged fields. It is an application policy.
func Merge(current, submitted, confirmed Profile) Profile {
	if current.Name != submitted.Name {
		confirmed.Name = current.Name
	}
	if current.Bio != submitted.Bio {
		confirmed.Bio = current.Bio
	}
	if current.Email != submitted.Email {
		confirmed.Email = current.Email
	}
	if current.Role != submitted.Role {
		confirmed.Role = current.Role
	}
	if current.Newsletter != submitted.Newsletter {
		confirmed.Newsletter = current.Newsletter
	}
	return confirmed
}
