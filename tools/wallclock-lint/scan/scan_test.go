// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scan

import (
	"strings"
	"testing"
)

func kinds(lines ...string) []string {
	var out []string
	for _, v := range violationsInFile("sample_test.go", lines) {
		out = append(out, v.Kind)
	}
	return out
}

func assertKinds(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("violation kinds = %v, want %v", got, want)
	}
}

// @decision: polling-audit
func TestAnUnclassifiedWaitFails(t *testing.T) {
	assertKinds(t, kinds("\t\tcase <-time.After(time.Second):"), KindUnclassified)
}

// @decision: polling-audit
func TestAnOutcomeWaitPasses(t *testing.T) {
	assertKinds(t, kinds(
		"\t\t//nolint:testwallclock-outcome the loop exits only on the awaited row appearing",
		"\t\tcase <-time.After(time.Second):",
	))
	assertKinds(t, kinds("\t\tcase <-time.After(time.Second): //nolint:testwallclock-outcome the loop exits only on success"))
}

// @decision: polling-audit
func TestAnOrderingDependentWaitFailsAndNamesTheEventLogTail(t *testing.T) {
	lines := []string{
		"\t\t//nolint:testwallclock-ordering this poll hopes to catch the node mid-flight",
		"\t\tcase <-time.After(time.Second):",
	}
	got := violationsInFile("sample_test.go", lines)
	if len(got) != 1 || got[0].Kind != KindOrdering {
		t.Fatalf("violations = %+v, want one %s", got, KindOrdering)
	}
	if !strings.Contains(got[0].Detail, "event-log tail") {
		t.Errorf("the ordering failure does not name the event-log tail: %s", got[0].Detail)
	}
	if got[0].Baselineable() {
		t.Errorf("an ordering-dependent wait is baselineable, so the backlog would absorb a new one")
	}
}

// @decision: polling-audit
func TestAPacingMarkerAdmitsAWaitThatIsNotAVerdictInput(t *testing.T) {
	assertKinds(t, kinds(
		"\t\t//nolint:testwallclock-pacing the stub simulates a template-declared delay; never a verdict input",
		"\t\tcase <-time.After(delay):",
	))
}

// @decision: test-wallclock-lint-ratchet
func TestNoClassRescuesAConstructThatFailsOnExpiry(t *testing.T) {
	for _, line := range []string{
		"\trequire.Eventually(t, cond, time.Second, time.Millisecond) //nolint:testwallclock-outcome it usually passes",
		"\trequire.Eventually(t, cond, time.Second, time.Millisecond) //nolint:testwallclock-pacing it usually passes",
		"\tfor time.Now().Before(deadline) { //nolint:testwallclock-outcome it usually passes",
		"\tfor time.Now().Before(deadline) { //nolint:testwallclock-pacing it usually passes",
		"\tfor time.Since(start) < time.Second { //nolint:testwallclock-outcome it usually passes",
		"\tfor time.Since(start) < time.Second { //nolint:testwallclock-pacing it usually passes",
	} {
		got := violationsInFile("sample_test.go", []string{line})
		if len(got) != 1 || got[0].Kind != KindInadmissible {
			t.Errorf("%q: violations = %+v, want one %s", line, got, KindInadmissible)
			continue
		}
		if got[0].Baselineable() {
			t.Errorf("%q: a class claim on an expiring construct is baselineable", line)
		}
	}
}

// @decision: test-wallclock-lint-ratchet
func TestATimeoutSelectArmThatEndsTheTestIsInadmissibleUnderAnyClass(t *testing.T) {
	for _, class := range []string{ClassOutcome, ClassPacing} {
		for _, arm := range []string{
			"\t\t\tt.Fatalf(\"timed out waiting for the callback\")",
			"\t\t\tt.Errorf(\"no dispatch arrived\")",
			"\t\t\trequire.Fail(t, \"no dispatch arrived\")",
			"\t\t\treturn fmt.Errorf(\"timed out waiting for the ack\")",
		} {
			lines := []string{
				"\t\t//nolint:testwallclock-" + class + " the send always arrives",
				"\t\tcase <-time.After(2 * time.Second):",
				arm,
				"\t\t}",
			}
			got := violationsInFile("sample_test.go", lines)
			if len(got) != 1 || got[0].Kind != KindInadmissible {
				t.Errorf("class %s, arm %q: violations = %+v, want one %s", class, arm, got, KindInadmissible)
				continue
			}
			if got[0].Baselineable() {
				t.Errorf("class %s, arm %q: a fail-on-timeout select is baselineable once it carries a marker", class, arm)
			}
		}
	}
}

// @decision: polling-audit
func TestATimeoutSelectArmThatOnlyPacesTheLoopIsAdmitted(t *testing.T) {
	assertKinds(t, kinds(
		"\t\t//nolint:testwallclock-outcome inter-poll pacing; the loop exits only on the awaited row appearing",
		"\t\tcase <-time.After(pollInterval):",
		"\t\t}",
	))
}

// @decision: polling-audit
func TestASleepDeclaresItsClassLikeEveryOtherWait(t *testing.T) {
	assertKinds(t, kinds("\t\ttime.Sleep(50 * time.Millisecond)"), KindUnclassified)
	assertKinds(t, kinds(
		"\t\t//nolint:testwallclock-pacing the stub simulates declared work; never a verdict input",
		"\t\ttime.Sleep(sc.delay)",
	))
	assertKinds(t, kinds("\t\ttime.Sleep(pollInterval) //nolint:testwallclock-outcome the loop exits only on success"))
	got := violationsInFile("sample_test.go", []string{
		"\t\t//nolint:testwallclock-ordering sleep long enough to catch the node mid-flight",
		"\t\ttime.Sleep(2 * time.Second)",
	})
	if len(got) != 1 || got[0].Kind != KindOrdering {
		t.Fatalf("violations = %+v, want one %s", got, KindOrdering)
	}
}

// @decision: test-wallclock-lint-ratchet
func TestAMarkerWithoutAKnownClassOrAJustificationFails(t *testing.T) {
	assertKinds(t, kinds("\t\t//nolint:testwallclock-eventual it will settle"), KindUnknownClass)
	assertKinds(t, kinds("\t\t//nolint:testwallclock-outcome"), KindNoJustifiation)
	assertKinds(t, kinds("\t\t//nolint:testwallclock-outcome   "), KindNoJustifiation)
}

// @decision: test-wallclock-lint-ratchet
func TestTheUnclassifiedSuppressionMarkerIsRetired(t *testing.T) {
	got := violationsInFile("sample_test.go", []string{"\t\t//nolint:testwallclock inter-poll pacing, not a verdict input"})
	if len(got) != 1 || got[0].Kind != KindLegacyMarker {
		t.Fatalf("violations = %+v, want one %s", got, KindLegacyMarker)
	}
	if got[0].Baselineable() {
		t.Errorf("the retired marker is baselineable, so the old idiom could return under the backlog")
	}
	for _, class := range Classes {
		if !strings.Contains(got[0].Detail, class) {
			t.Errorf("the retirement message does not name the %s class: %s", class, got[0].Detail)
		}
	}
}

// @decision: test-wallclock-lint-ratchet
func TestOnlyTheUnclassifiedBacklogIsBaselineable(t *testing.T) {
	baselineable := map[string]bool{
		KindUnclassified:   true,
		KindOrdering:       false,
		KindUnknownClass:   false,
		KindNoJustifiation: false,
		KindLegacyMarker:   false,
		KindInadmissible:   false,
	}
	for kind, want := range baselineable {
		if got := (Violation{Kind: kind}).Baselineable(); got != want {
			t.Errorf("Violation{Kind: %q}.Baselineable() = %v, want %v", kind, got, want)
		}
	}
	counts := CountsByFile([]Violation{
		{File: "a_test.go", Kind: KindUnclassified},
		{File: "a_test.go", Kind: KindOrdering},
		{File: "b_test.go", Kind: KindLegacyMarker},
	})
	if counts["a_test.go"] != 1 || len(counts) != 1 {
		t.Errorf("CountsByFile = %v, want the unclassified backlog alone", counts)
	}
}

// @decision: test-wallclock-lint-ratchet
func TestTheScannerSkipsItsOwnContractFixturesAndScansOrdinaryTestCode(t *testing.T) {
	if isTestCode(ScannerOwnPackage + "scan_test.go") {
		t.Errorf("the scanner reads its own contract fixtures as waits; every marker and construct in this file is input data, not a wait")
	}
	for _, rel := range []string{
		"lib/runtime/breakpoint_eval_test.go",
		"test/support/composestub/main.go",
		"lib/foundation/persistence/conformance/migrations.go",
	} {
		if !isTestCode(rel) {
			t.Errorf("%s is test code the scanner no longer reads", rel)
		}
	}
}
