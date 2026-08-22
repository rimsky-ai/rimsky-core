// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

const lockPathFieldSuffix = "LockPath"

// @concept: advisory-lock
func TestPinnedAdvisoryLockFilesArePairwiseDistinct(t *testing.T) {
	t.Parallel()
	const dbPath = "/tmp/advisory-lock-paths/state.db"
	locker := newAdvisoryLocker(dbPath)

	paths := lockPathsOf(t, locker)
	if len(paths) == 0 {
		t.Fatalf("advisoryLockerImpl declares no %s field; the check would pass over an empty population",
			lockPathFieldSuffix)
	}
	paths["database"] = dbPath

	seen := map[string]string{}
	for _, name := range sortedFieldNames(paths) {
		path := paths[name]
		if path == "" {
			t.Fatalf("advisory-lock file %s has no path; newAdvisoryLocker leaves it unset", name)
		}
		if prior, clash := seen[path]; clash {
			t.Fatalf("advisory-lock files %s and %s share the path %s; two locks sharing a file exclude each other",
				prior, name, path)
		}
		seen[path] = name
	}
	t.Logf("checked all %d pinned paths advisoryLockerImpl holds plus the database file, the whole population "+
		"its %s fields declare: %s", len(paths)-1, lockPathFieldSuffix, strings.Join(sortedFieldNames(paths), " "))
}

func lockPathsOf(t *testing.T, locker *advisoryLockerImpl) map[string]string {
	t.Helper()
	out := map[string]string{}
	value := reflect.ValueOf(locker).Elem()
	structType := value.Type()
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		if !strings.HasSuffix(field.Name, lockPathFieldSuffix) {
			continue
		}
		if field.Type.Kind() != reflect.String {
			t.Fatalf("advisoryLockerImpl.%s names a lock path and holds a %s, not a string",
				field.Name, field.Type.Kind())
		}
		out[field.Name] = value.Field(i).String()
	}
	return out
}

func sortedFieldNames(paths map[string]string) []string {
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
