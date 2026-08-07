// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var protoSanctionedComment = regexp.MustCompile(`Copyright|Licensed under|SPDX|@concept:|@story:|@decision:`)

func commentIndexOutsideStrings(line string) int {
	inString := false
	for i := 0; i < len(line)-1; i++ {
		switch {
		case line[i] == '"':
			inString = !inString
		case !inString && line[i] == '/' && line[i+1] == '/':
			return i
		}
	}
	return -1
}

// @decision: coding-style
func TestProtoSourcesCarryNoProseComments(t *testing.T) {
	protoDir := filepath.Join(findRepoRoot(t), "lib", "protocols", "proto", "v1")
	entries, err := os.ReadDir(protoDir)
	if err != nil {
		t.Fatalf("read %s: %v", protoDir, err)
	}
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".proto") {
			continue
		}
		checked++
		src, err := os.ReadFile(filepath.Join(protoDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for n, line := range strings.Split(string(src), "\n") {
			idx := commentIndexOutsideStrings(line)
			if idx < 0 {
				continue
			}
			comment := line[idx:]
			if strings.TrimSpace(line[:idx]) == "" && protoSanctionedComment.MatchString(comment) {
				continue
			}
			t.Errorf("%s:%d: prose comment in a proto source — proto files carry only license headers and configured citation tags; truth belongs in the design corpus, names, or tests: %s",
				e.Name(), n+1, strings.TrimSpace(line))
		}
	}
	if checked == 0 {
		t.Fatal("no .proto files found under lib/protocols/proto/v1 — the hygiene pin is checking nothing")
	}
}
