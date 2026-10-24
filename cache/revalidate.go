package cache

import (
	"errors"
	"time"
)

var ErrRevalidationWindow = errors.New("cache: stale revalidation window must be nonnegative or cache.Never")

// MarkStaleAll is MarkStaleKey for every retained or loading key.
func (s *Store) MarkStaleAll(window time.Duration) (int, error) {
	return s.markStale(func(string, map[string]struct{}, map[string]struct{}) bool { return true }, window)
}

// MarkStaleKey requests refresh on the next read while allowing the last good
// entry until the earlier of its existing hard expiry and now+window. Repeated
// marking never extends that deadline or resets its generation/browser age.
// Never imposes no additional deadline; it preserves any existing hard expiry.
// Zero is strict expiration. Other negative windows return an error unchanged.
// Matching in-progress/detached/undelivered generations are retired so a load
// begun before the committed write cannot publish afterward. Loaders must be
// read-only/cancellation-aware. Return counts distinct affected keys.
func (s *Store) MarkStaleKey(key string, window time.Duration) (int, error) {
	return s.markStale(func(candidate string, _, _ map[string]struct{}) bool { return candidate == key }, window)
}

// MarkStaleTag is MarkStaleKey for every entry/load carrying this exact tag.
func (s *Store) MarkStaleTag(tag string, window time.Duration) (int, error) {
	return s.markStale(func(_ string, tags, _ map[string]struct{}) bool { _, ok := tags[tag]; return ok }, window)
}

// MarkStalePath marks the normalized exact path label.
func (s *Store) MarkStalePath(path string, window time.Duration) (int, error) {
	path = cleanPath(path)
	return s.markStale(func(_ string, _, paths map[string]struct{}) bool { _, ok := paths[path]; return ok }, window)
}

// MarkStalePathPrefix marks path labels at or beneath a normalized prefix.
func (s *Store) MarkStalePathPrefix(prefix string, window time.Duration) (int, error) {
	return s.markStale(pathPrefixMatch(cleanPath(prefix)), window)
}

func (s *Store) markStale(match func(string, map[string]struct{}, map[string]struct{}) bool, window time.Duration) (int, error) {
	if window < 0 && window != Never {
		return 0, ErrRevalidationWindow
	}
	return s.changeEntries(match, true, &window), nil
}
