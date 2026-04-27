package filesystem

import (
	"path/filepath"
	"strings"
)

// RegionsConflict reports whether two filesystem regions overlap. A region
// is a list of path globs; two regions conflict if any glob in one overlaps
// with any glob in the other.
//
// Pure: no filesystem reads, no external state. The supervisor calls this
// inside the atomic-acquisition transaction (spec §13.3) and inside hot
// eligibility loops; impurity here would corrupt acquisition correctness.
func RegionsConflict(a, b []string) bool {
	for _, ga := range a {
		for _, gb := range b {
			if globsOverlap(ga, gb) {
				return true
			}
		}
	}
	return false
}

// globsOverlap reports whether two path globs can match a common path.
//
// The check is heuristic-but-conservative: when in doubt, return true so
// the supervisor refuses the conflicting acquisition. Concretely:
//
//   - Two literal (no-meta) paths overlap iff they're equal.
//   - A literal path overlaps a glob iff the glob matches the literal.
//   - Two globs overlap iff one matches the other treated as a path, OR
//     their fixed prefixes are compatible (one is a prefix of the other,
//     accounting for "**" wildcards which match any path under their
//     prefix).
//
// "**" semantics: a glob containing "**" matches any path under the
// prefix preceding the first "**". `path/filepath.Match` does not
// understand "**"; we expand it manually by truncating to the prefix
// (removing the trailing "/" if present) and checking prefix containment
// for the literal-vs-glob and glob-vs-glob cases.
func globsOverlap(a, b string) bool {
	if a == b {
		return true
	}

	aHasMeta := hasGlobMeta(a)
	bHasMeta := hasGlobMeta(b)

	// Literal-vs-literal: only equal paths overlap.
	if !aHasMeta && !bHasMeta {
		return a == b
	}

	// Literal-vs-glob: ask the glob whether it matches the literal.
	if !aHasMeta {
		return globMatchesPath(b, a)
	}
	if !bHasMeta {
		return globMatchesPath(a, b)
	}

	// Glob-vs-glob: try literal-substitution in both directions, then
	// fall back to fixed-prefix compatibility.
	if globMatchesPath(a, b) || globMatchesPath(b, a) {
		return true
	}
	return prefixesCompatible(a, b)
}

// hasGlobMeta reports whether s contains any glob metacharacter recognized
// by `path/filepath.Match` ("*", "?", "[") or our extended "**".
func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// globMatchesPath reports whether glob matches path under our extended
// semantics. Splits "**" away first: if glob contains "**", the prefix
// before it is required to be a path-prefix of path; the suffix after is
// matched against the trailing portion via filepath.Match (suffix
// without "**" expansion).
func globMatchesPath(glob, path string) bool {
	if !strings.Contains(glob, "**") {
		ok, err := filepath.Match(glob, path)
		if err != nil {
			// Malformed pattern → conservative: assume overlap.
			return true
		}
		return ok
	}

	// "**" present: any path under the prefix matches.
	idx := strings.Index(glob, "**")
	prefix := glob[:idx]
	prefix = strings.TrimRight(prefix, "/")
	if prefix == "" {
		// "**/foo" or "**foo" — matches anything containing/ending the
		// suffix. Conservative: treat as match.
		return true
	}
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+"/")
}

// prefixesCompatible reports whether two globs share a fixed prefix that
// admits a common matching path. Used as the last-resort branch in
// glob-vs-glob comparison when neither glob can be substituted into the
// other.
//
// Algorithm: take each glob's fixed prefix (the substring up to the first
// glob metacharacter). If one is a path-prefix of the other (or they're
// equal), the globs may overlap and we return true. Otherwise return
// false — the globs target disjoint subtrees.
func prefixesCompatible(a, b string) bool {
	pa := fixedPrefix(a)
	pb := fixedPrefix(b)
	if pa == pb {
		return true
	}
	if pa == "" || pb == "" {
		return true
	}
	return isPathPrefix(pa, pb) || isPathPrefix(pb, pa)
}

// fixedPrefix returns the leading substring of glob up to (but not
// including) the first glob metacharacter, trimmed of any trailing "/".
func fixedPrefix(glob string) string {
	idx := strings.IndexAny(glob, "*?[")
	if idx < 0 {
		return strings.TrimRight(glob, "/")
	}
	return strings.TrimRight(glob[:idx], "/")
}

// isPathPrefix reports whether prefix is a path-component prefix of full.
// Empty prefix is a prefix of everything; otherwise full must equal
// prefix or start with prefix+"/".
func isPathPrefix(prefix, full string) bool {
	if prefix == "" {
		return true
	}
	if prefix == full {
		return true
	}
	return strings.HasPrefix(full, prefix+"/")
}
