package syncbundle

import (
	"fmt"
	"sort"
	"strings"
)

// Match the operating system's bounded symlink traversal rather than accepting
// a namespace that cannot be followed by the Linux runner.
const maxSymlinkHops = 40

func validateSymlinkContainment(manifest Manifest) error {
	baseline := make(map[string]Entry, len(manifest.Baseline))
	for _, entry := range manifest.Baseline {
		baseline[entry.Path] = entry
	}
	// The baseline is materialized and committed before the overlay is applied.
	// An unsafe baseline must therefore be rejected even when the overlay would
	// delete or replace its links.
	if err := validateSymlinkNamespace("baseline", baseline); err != nil {
		return err
	}
	workspace := make(map[string]Entry, len(baseline)+len(manifest.Workspace))
	for rel, entry := range baseline {
		workspace[rel] = entry
	}
	for _, rel := range manifest.Deleted {
		// Manifest validation requires each deleted path to be a baseline leaf;
		// prefix conflicts prevent it from also having baseline descendants.
		delete(workspace, rel)
	}
	for _, entry := range manifest.Workspace {
		workspace[entry.Path] = entry
	}
	return validateSymlinkNamespace("workspace", workspace)
}

type sourcePathNode struct {
	parent   *sourcePathNode
	children map[string]*sourcePathNode
	entry    *Entry
}

type symlinkResolution struct {
	target *sourcePathNode
	hops   int
}

type symlinkResolver struct {
	resolved map[*sourcePathNode]symlinkResolution
	active   map[*sourcePathNode]bool
}

func validateSymlinkNamespace(group string, entries map[string]Entry) error {
	var links []string
	for rel, entry := range entries {
		if entry.Type == EntryTypeSymlink {
			links = append(links, rel)
		}
	}
	if len(links) == 0 {
		return nil
	}
	sort.Strings(links)
	// A trie lets each component and parent step take constant time. Repeated
	// string joins make a long shared target quadratic, even within the manifest
	// byte limit. Missing targets use temporary nodes, never the host filesystem.
	root := &sourcePathNode{}
	leaves := make(map[string]*sourcePathNode, len(links))
	for rel, entry := range entries {
		current := root
		for _, part := range strings.Split(rel, "/") {
			if current.children == nil {
				current.children = make(map[string]*sourcePathNode)
			}
			next := current.children[part]
			if next == nil {
				next = &sourcePathNode{parent: current}
				current.children[part] = next
			}
			current = next
		}
		item := entry
		current.entry = &item
		if entry.Type == EntryTypeSymlink {
			leaves[rel] = current
		}
	}
	resolver := symlinkResolver{resolved: make(map[*sourcePathNode]symlinkResolution, len(links)), active: make(map[*sourcePathNode]bool)}
	for _, rel := range links {
		if _, err := resolver.resolveLink(leaves[rel]); err != nil {
			return fmt.Errorf("%s symlink %q has invalid target: %w", group, rel, err)
		}
	}
	return nil
}

func (resolver *symlinkResolver) resolveLink(link *sourcePathNode) (symlinkResolution, error) {
	if resolver.active[link] {
		return symlinkResolution{}, fmt.Errorf("target contains symlink cycle at %q", link.entry.Path)
	}
	if resolved, ok := resolver.resolved[link]; ok {
		return resolved, nil
	}
	resolver.active[link] = true
	defer delete(resolver.active, link)
	// Bound recursive call depth as well as the total traversal count. A long
	// chain must fail before it can exhaust the validator's stack.
	if len(resolver.active) > maxSymlinkHops {
		return symlinkResolution{}, fmt.Errorf("target exceeds %d symlink hops", maxSymlinkHops)
	}
	resolved, err := resolver.resolveTarget(link.parent, strings.Split(link.entry.Linkname, "/"))
	if err != nil {
		return symlinkResolution{}, err
	}
	resolved.hops++
	if resolved.hops > maxSymlinkHops {
		return symlinkResolution{}, fmt.Errorf("target exceeds %d symlink hops", maxSymlinkHops)
	}
	resolver.resolved[link] = resolved
	return resolved, nil
}

// Resolve target components in filesystem order. Cleaning a path first would
// incorrectly cancel parent components against symlinks: "alias/../file" must
// expand alias before interpreting "..". The namespace is the sealed source,
// never the caller's filesystem. Missing internal targets remain valid dangling
// links, but neither existing nor dangling paths may escape the workspace.
func (resolver *symlinkResolver) resolveTarget(current *sourcePathNode, parts []string) (symlinkResolution, error) {
	hops := 0
	for _, part := range parts {
		if current.entry != nil && current.entry.Type == EntryTypeFile {
			return symlinkResolution{}, fmt.Errorf("target traverses regular file %q as a directory", current.entry.Path)
		}
		switch part {
		case "", ".":
			continue
		case "..":
			if current.parent == nil {
				return symlinkResolution{}, fmt.Errorf("target escapes workspace")
			}
			current = current.parent
			continue
		}
		if strings.EqualFold(part, ".git") {
			return symlinkResolution{}, fmt.Errorf("target traverses git control metadata")
		}
		next := current.children[part]
		if next == nil {
			current = &sourcePathNode{parent: current}
			continue
		}
		if next.entry == nil || next.entry.Type != EntryTypeSymlink {
			current = next
			continue
		}
		resolved, err := resolver.resolveLink(next)
		if err != nil {
			return symlinkResolution{}, err
		}
		hops += resolved.hops
		if hops >= maxSymlinkHops {
			// resolveLink still needs to count the link whose target we are reading.
			return symlinkResolution{}, fmt.Errorf("target exceeds %d symlink hops", maxSymlinkHops)
		}
		current = resolved.target
	}
	return symlinkResolution{target: current, hops: hops}, nil
}
