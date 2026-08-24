// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: test-wallclock-lint-ratchet
// @decision: polling-audit

package scan

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const MarkerPrefix = "//nolint:testwallclock-"

const LegacyMarker = "//nolint:testwallclock "

const (
	ClassOutcome  = "outcome"
	ClassOrdering = "ordering"
	ClassPacing   = "pacing"
)

var Classes = []string{ClassOutcome, ClassOrdering, ClassPacing}

const (
	KindUnclassified   = "unclassified-wait"
	KindPackageState   = "test-writes-package-state"
	KindOrdering       = "ordering-dependent-wait"
	KindUnknownClass   = "unknown-wait-class"
	KindNoJustifiation = "unjustified-marker"
	KindLegacyMarker   = "legacy-marker"
	KindInadmissible   = "inadmissible-under-any-class"
)

var detectors = []struct {
	Name           string
	PacesAPoll     bool
	ReadsSelectArm bool
	Pattern        *regexp.Regexp
}{
	{"eventually", false, false, regexp.MustCompile(`\b(require|assert)\.(Eventually|EventuallyWithT|Eventuallyf|EventuallyWithTf|Never|Neverf)\(`)},
	{"timeout-select", true, true, regexp.MustCompile(`case\s+<-time\.After\(`)},
	{"sleep", true, false, regexp.MustCompile(`\btime\.Sleep\(`)},
	{"deadline-poll", false, false, regexp.MustCompile(`for\s+time\.Now\(\)\.Before\(`)},
	{"deadline-poll", false, false, regexp.MustCompile(`for\s+time\.Since\(`)},
	{"context-deadline", true, false, regexp.MustCompile(`context\.With(Timeout|Deadline)\(`)},
}

var armFailsTest = []*regexp.Regexp{
	regexp.MustCompile(`\.(Fatal|Fatalf|FailNow|Errorf|Skipf?)\(`),
	regexp.MustCompile(`\.Error\(\s*[^)\s]`),
	regexp.MustCompile(`\b(require|assert)\.`),
	regexp.MustCompile(`(?i)(errors\.New|fmt\.Errorf)\([^)]*(timed out|timeout|deadline|took too long|never )`),
}

func selectArmEndsTheTest(lines []string, at int) bool {
	body := []string{}
	if colon := strings.LastIndex(lines[at], ":"); colon >= 0 {
		body = append(body, lines[at][colon+1:])
	}
	for j := at + 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if strings.HasPrefix(t, "case ") || strings.HasPrefix(t, "case<-") ||
			strings.HasPrefix(t, "default:") || strings.HasPrefix(t, "}") {
			break
		}
		body = append(body, lines[j])
	}
	joined := strings.Join(body, "\n")
	for _, re := range armFailsTest {
		if re.MatchString(joined) {
			return true
		}
	}
	return false
}

var scanRoots = []string{"cmd", "lib", "test", "tools"}

const ScannerOwnPackage = "tools/wallclock-lint/scan/"

func isTestCode(rel string) bool {
	if !strings.HasSuffix(rel, ".go") {
		return false
	}
	if strings.HasPrefix(filepath.ToSlash(rel), ScannerOwnPackage) {
		return false
	}
	if strings.HasSuffix(rel, "_test.go") {
		return true
	}
	if strings.HasPrefix(filepath.ToSlash(rel), "lib/foundation/persistence/conformance/") {
		return true
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, p := range parts[:len(parts)-1] {
		if p == "test" || p == "testfixture" || p == "storetest" || p == "testdata" {
			return true
		}
	}
	return false
}

type marker struct {
	present       bool
	class         string
	justification string
}

func parseMarker(line string) marker {
	idx := strings.Index(line, MarkerPrefix)
	if idx < 0 {
		return marker{}
	}
	rest := line[idx+len(MarkerPrefix):]
	cut := strings.IndexFunc(rest, func(r rune) bool { return r == ' ' || r == '\t' })
	if cut < 0 {
		return marker{present: true, class: rest}
	}
	return marker{present: true, class: rest[:cut], justification: strings.TrimSpace(rest[cut:])}
}

func knownClass(class string) bool {
	for _, c := range Classes {
		if c == class {
			return true
		}
	}
	return false
}

type Violation struct {
	File   string
	Line   int
	Kind   string
	Detail string
	Source string
}

func (v Violation) Baselineable() bool {
	return v.Kind == KindUnclassified
}

func TestCodeViolations(repoRoot string) ([]Violation, error) {
	byDir := map[string]map[string]string{}
	for _, root := range scanRoots {
		rootPath := filepath.Join(repoRoot, root)
		if _, err := os.Stat(rootPath); err != nil {
			continue
		}
		err := filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "gen" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			rel, rerr := filepath.Rel(repoRoot, path)
			if rerr != nil {
				return rerr
			}
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, ScannerOwnPackage) {
				return nil
			}
			src, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			dir := filepath.ToSlash(filepath.Dir(rel))
			if byDir[dir] == nil {
				byDir[dir] = map[string]string{}
			}
			byDir[dir][rel] = string(src)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	var out []Violation
	for _, sources := range byDir {
		holdsTestCode := false
		for rel, src := range sources {
			if !isTestCode(rel) {
				continue
			}
			holdsTestCode = true
			out = append(out, violationsInFile(rel, strings.Split(src, "\n"))...)
		}
		if holdsTestCode {
			out = append(out, packageStateViolations(sources)...)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

func violationsInFile(rel string, lines []string) []Violation {
	var out []Violation
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(line, LegacyMarker) && !strings.Contains(line, MarkerPrefix) {
			out = append(out, Violation{File: rel, Line: i + 1, Kind: KindLegacyMarker,
				Detail: fmt.Sprintf("the unclassified suppression marker is retired; write %s<class> with a justification, where <class> is one of %s",
					MarkerPrefix, strings.Join(Classes, ", ")),
				Source: trimmed})
			continue
		}

		own := parseMarker(line)
		var above marker
		if i > 0 {
			above = parseMarker(lines[i-1])
		}
		m := own
		if !m.present {
			m = above
		}

		if own.present {
			if !knownClass(own.class) {
				out = append(out, Violation{File: rel, Line: i + 1, Kind: KindUnknownClass,
					Detail: fmt.Sprintf("wait class %q is not one of %s", own.class, strings.Join(Classes, ", ")),
					Source: trimmed})
				continue
			}
			if own.justification == "" {
				out = append(out, Violation{File: rel, Line: i + 1, Kind: KindNoJustifiation,
					Detail: fmt.Sprintf("the %s marker carries no justification", own.class),
					Source: trimmed})
				continue
			}
			if own.class == ClassOrdering {
				out = append(out, Violation{File: rel, Line: i + 1, Kind: KindOrdering,
					Detail: "an ordering-dependent wait can sample around the transition it waits on; block on the event-log tail, the durable record of that transition, instead",
					Source: trimmed})
				continue
			}
		}

		for _, det := range detectors {
			if !det.Pattern.MatchString(line) {
				continue
			}
			paces := det.PacesAPoll
			if det.ReadsSelectArm && selectArmEndsTheTest(lines, i) {
				paces = false
			}
			switch {
			case !m.present:
				out = append(out, Violation{File: rel, Line: i + 1, Kind: KindUnclassified,
					Detail: fmt.Sprintf("%s: a deadline that fails a test is a load-dependent verdict; block on the awaited signal, poll until success, or declare the class with %s%s",
						det.Name, MarkerPrefix, ClassOutcome),
					Source: trimmed})
			case !paces:
				out = append(out, Violation{File: rel, Line: i + 1, Kind: KindInadmissible,
					Detail: fmt.Sprintf("%s ends the test when its deadline expires, so no class admits it. Both %q and %q claim the elapsed time is not the verdict, and this construct makes it the verdict. Block on the awaited signal or on the event-log tail instead",
						det.Name, ClassOutcome, ClassPacing),
					Source: trimmed})
			}
		}
	}
	return out
}

func CountsByFile(violations []Violation) map[string]int {
	counts := map[string]int{}
	for _, v := range violations {
		if !v.Baselineable() {
			continue
		}
		counts[v.File]++
	}
	return counts
}

func NonBaselineable(violations []Violation) []Violation {
	var out []Violation
	for _, v := range violations {
		if !v.Baselineable() {
			out = append(out, v)
		}
	}
	return out
}
