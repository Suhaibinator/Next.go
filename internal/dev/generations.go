package dev

import "path/filepath"

// Keep the active generation plus seven predecessors so rebuild transitions
// have a grace window without retaining every compiler output indefinitely.
const retainedBuilds = 8

// Caller holds s.mu. Only directories recorded from successful Compile results
// are eligible for cleanup; assets paths are never interpreted as parent paths.
func (s *supervisor) retainBuildLocked(id, dir string) []string {
	return s.retainBuildAssetsLocked(id, dir, map[string]string{id: filepath.Join(dir, "server", "assets")})
}

// One retained compiler generation can contain several independently hashed
// zone runtimes. Retention counts generations, not individual zone bundles.
func (s *supervisor) retainBuildAssetsLocked(id, dir string, assets map[string]string) []string {
	return s.retainBuildRuntimeLocked(id, dir, assets, nil)
}

func (s *supervisor) retainBuildRuntimeLocked(id, dir string, assets map[string]string, owners map[string][]assetOwner) []string {
	if s.assets == nil {
		s.assets = make(map[string]string)
	}
	if s.buildDirs == nil {
		s.buildDirs = make(map[string]string)
	}
	if s.buildAssets == nil {
		s.buildAssets = make(map[string]map[string]string)
	}
	if s.buildOwners == nil {
		s.buildOwners = make(map[string]map[string][]assetOwner)
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
	owned := make(map[string]string, len(assets))
	for runtime, path := range assets {
		owned[runtime] = path
	}
	s.buildAssets[id] = owned
	ownerCopy := make(map[string][]assetOwner, len(owners))
	for runtime, entries := range owners {
		ownerCopy[runtime] = append([]assetOwner(nil), entries...)
	}
	s.buildOwners[id] = ownerCopy
	for len(s.buildOrder) > retainedBuilds {
		index := -1
		for i, key := range s.buildOrder {
			if !s.pinnedBuilds[key] {
				index = i
				break
			}
		}
		if index < 0 {
			break
		}
		oldest := s.buildOrder[index]
		s.buildOrder = append(s.buildOrder[:index], s.buildOrder[index+1:]...)
		if old := s.buildDirs[oldest]; old != "" {
			expired = append(expired, old)
		}
		delete(s.buildDirs, oldest)
		delete(s.buildAssets, oldest)
		delete(s.buildOwners, oldest)
	}
	// Newer retained copies win shared runtime IDs. Deleting an expired build
	// cannot leave an asset pointer into its removed directory.
	s.assets = make(map[string]string)
	s.assetOwners = make(map[string][]assetOwner)
	for _, generation := range s.buildOrder {
		for runtime, path := range s.buildAssets[generation] {
			s.assets[runtime] = path
		}
		for runtime, entries := range s.buildOwners[generation] {
			s.assetOwners[runtime] = append(s.assetOwners[runtime], entries...)
		}
	}
	// Environment-only publications share compiler output. Its directory stays
	// owned until the final retained publication referencing it expires.
	var removable []string
	seen := map[string]bool{}
	for _, dir := range expired {
		used := false
		for _, current := range s.buildDirs {
			if current == dir {
				used = true
				break
			}
		}
		if !used && !seen[dir] {
			removable = append(removable, dir)
			seen[dir] = true
		}
	}
	return removable
}
