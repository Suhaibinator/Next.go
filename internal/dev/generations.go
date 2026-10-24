package dev

import "path/filepath"

// Keep the active generation plus seven predecessors so rebuild transitions
// have a grace window without retaining every compiler output indefinitely.
const retainedBuilds = 8

// Caller holds s.mu. Only directories recorded from successful Compile results
// are eligible for cleanup; assets paths are never interpreted as parent paths.
func (s *supervisor) retainBuildLocked(id, dir string) []string {
	if s.assets == nil {
		s.assets = make(map[string]string)
	}
	if s.buildDirs == nil {
		s.buildDirs = make(map[string]string)
	}
	var expired []string
	if old := s.buildDirs[id]; old != "" && old != dir {
		expired = append(expired, old)
	}
	order := s.buildOrder[:0]
	for _, previous := range s.buildOrder {
		if previous != id {
			order = append(order, previous)
		}
	}
	s.buildOrder = append(order, id)
	s.buildDirs[id] = dir
	s.assets[id] = filepath.Join(dir, "server", "assets")
	for len(s.buildOrder) > retainedBuilds {
		oldest := s.buildOrder[0]
		s.buildOrder = s.buildOrder[1:]
		if old := s.buildDirs[oldest]; old != "" {
			expired = append(expired, old)
		}
		delete(s.buildDirs, oldest)
		delete(s.assets, oldest)
	}
	return expired
}
