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
		KindPackageState:   false,
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

// @decision: test-wallclock-lint-ratchet
func TestAContextDeadlineWhoseExpiryFeedsTheVerdictFails(t *testing.T) {
	lines := []string{
		"func TestSlowCall(t *testing.T) {",
		"\tctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)",
		"\tdefer cancel()",
		"\t_, err := call(ctx)",
		"\tif !errors.Is(err, context.DeadlineExceeded) {",
		"\t\tt.Fatalf(\"err = %v\", err)",
		"\t}",
		"}",
	}
	got := violationsInFile("sample_test.go", lines)
	if len(got) != 1 || got[0].Kind != KindUnclassified {
		t.Fatalf("violations = %+v, want one %s", got, KindUnclassified)
	}
	if !strings.Contains(got[0].Detail, "context-deadline") {
		t.Errorf("the failure does not name the construct: %s", got[0].Detail)
	}
}

// @decision: test-wallclock-lint-ratchet
func TestEveryContextDeadlineIsRead(t *testing.T) {
	assertKinds(t, kinds(
		"func TestBootsTheStack(t *testing.T) {",
		"\tctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)",
		"\tdefer cancel()",
		"\tstack := boot(ctx, t)",
		"\trequire.NotNil(t, stack)",
		"}",
	), KindUnclassified)
}

// @decision: test-wallclock-lint-ratchet
func TestATeardownGraceIsAdmittedWithAPacingMarker(t *testing.T) {
	assertKinds(t, kinds(
		"func TestBootsTheStack(t *testing.T) {",
		"\tt.Cleanup(func() {",
		"\t\t//nolint:testwallclock-pacing the teardown discards the terminate error, so no verdict reads this grace",
		"\t\ttermCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)",
		"\t\tdefer cancel()",
		"\t\t_ = c.Terminate(termCtx)",
		"\t})",
		"}",
	))
}

// @decision: test-wallclock-lint-ratchet
func TestAContextDeadlineUnderTestIsAdmittedWithAClassMarker(t *testing.T) {
	assertKinds(t, kinds(
		"func TestDeadlinePropagates(t *testing.T) {",
		"\t//nolint:testwallclock-outcome the deadline is the input under test; the fake clock decides when it expires",
		"\tctx, cancel := context.WithDeadline(context.Background(), clock.Now().Add(time.Second))",
		"\tdefer cancel()",
		"\trequire.ErrorIs(t, call(ctx), context.DeadlineExceeded)",
		"}",
	))
}

// @decision: test-wallclock-lint-ratchet
func TestATestThatWritesAPackageLevelVariableFails(t *testing.T) {
	got := packageStateViolations(map[string]string{
		"prod.go": "package sample\n\nvar binaryDir = \"/usr/local/bin\"\n",
		"sample_test.go": "package sample\n\nimport \"testing\"\n\n" +
			"func TestSpawn(t *testing.T) {\n\tbinaryDir = t.TempDir()\n}\n",
	})
	if len(got) != 1 || got[0].Kind != KindPackageState {
		t.Fatalf("violations = %+v, want one %s", got, KindPackageState)
	}
	if !strings.Contains(got[0].Detail, "binaryDir") || !strings.Contains(got[0].Detail, "TestSpawn") {
		t.Errorf("the failure names neither the variable nor the test: %s", got[0].Detail)
	}
	if got[0].Line != 6 {
		t.Errorf("violation line = %d, want the line of the write (6)", got[0].Line)
	}
}

// @decision: test-wallclock-lint-ratchet
func TestATestingTBHelperWritingPackageStateFailsAndAShadowingLocalDoesNot(t *testing.T) {
	got := packageStateViolations(map[string]string{
		"prod.go": "package sample\n\nvar registry = map[string]int{}\n",
		"sample_test.go": "package sample\n\nimport \"testing\"\n\n" +
			"func register(t testing.TB, name string) {\n\tregistry[name] = 1\n}\n\n" +
			"func TestShadow(t *testing.T) {\n\tregistry := map[string]int{}\n\tregistry[\"a\"] = 1\n\t_ = registry\n}\n",
	})
	if len(got) != 1 || got[0].Kind != KindPackageState {
		t.Fatalf("violations = %+v, want one %s (the helper's write, not the shadowing local's)", got, KindPackageState)
	}
	if !strings.Contains(got[0].Detail, "register") {
		t.Errorf("the failure does not name the helper that wrote package state: %s", got[0].Detail)
	}
}

// @decision: test-wallclock-lint-ratchet
func TestNoWaitClassAdmitsAWriteToPackageState(t *testing.T) {
	for _, class := range []string{ClassOutcome, ClassPacing} {
		got := packageStateViolations(map[string]string{
			"prod.go": "package sample\n\nvar registry = map[string]int{}\n",
			"sample_test.go": "package sample\n\nimport \"testing\"\n\n" +
				"func TestWrites(t *testing.T) {\n\tregistry[\"a\"] = 1 //nolint:testwallclock-" + class + " it settles\n}\n",
		})
		if len(got) != 1 || got[0].Kind != KindInadmissible {
			t.Fatalf("class %s: violations = %+v, want one %s", class, got, KindInadmissible)
		}
		if got[0].Baselineable() {
			t.Errorf("class %s: a class claim over shared package state is baselineable", class)
		}
	}
}

// @decision: test-wallclock-lint-ratchet
func TestATestThatWritesPackageStateThroughACallFails(t *testing.T) {
	got := packageStateViolations(map[string]string{
		"prod.go": "package sample\n\nvar registry = map[string]int{}\n\n" +
			"func Register(name string) {\n\tregistry[name] = 1\n}\n",
		"sample_test.go": "package sample\n\nimport \"testing\"\n\nfunc TestRegister(t *testing.T) {\n\tRegister(\"a\")\n}\n",
	})
	if len(got) != 1 || got[0].Kind != KindPackageState {
		t.Fatalf("violations = %+v, want one %s — a setter launders the write; it does not remove it", got, KindPackageState)
	}
	if !strings.Contains(got[0].Detail, "registry") || !strings.Contains(got[0].Detail, "Register") ||
		!strings.Contains(got[0].Detail, "TestRegister") {
		t.Errorf("the failure names neither the variable, the setter, nor the test: %s", got[0].Detail)
	}
	if got[0].Line != 6 {
		t.Errorf("violation line = %d, want the line of the call (6)", got[0].Line)
	}
}

// @decision: test-wallclock-lint-ratchet
func TestAWriteLaunderedThroughTwoHelpersIsStillTheTestsWrite(t *testing.T) {
	got := packageStateViolations(map[string]string{
		"prod.go": "package sample\n\nvar pool *int\n\n" +
			"func SetPool(p *int) {\n\tpool = p\n}\n\n" +
			"func SetPoolForTesting(p *int) {\n\tSetPool(p)\n}\n",
		"sample_test.go": "package sample\n\nimport \"testing\"\n\n" +
			"func TestPool(t *testing.T) {\n\tn := 1\n\tSetPoolForTesting(&n)\n}\n",
	})
	if len(got) != 1 || got[0].Kind != KindPackageState {
		t.Fatalf("violations = %+v, want one %s — a wrapper around a setter launders nothing", got, KindPackageState)
	}
}

// @decision: test-wallclock-lint-ratchet
func TestProductStateTheTestHandsNothingIntoIsNotTheTestsWrite(t *testing.T) {
	got := packageStateViolations(map[string]string{
		"prod.go": "package sample\n\nimport \"sync\"\n\n" +
			"var (\n\tonce sync.Once\n\tnetwork string\n)\n\n" +
			"func NetworkName(ctx int) string {\n\tonce.Do(func() {\n\t\tnetwork = newNetwork(ctx)\n\t})\n\treturn network\n}\n\n" +
			"func newNetwork(int) string {\n\treturn \"n\"\n}\n",
		"sample_test.go": "package sample\n\nimport \"testing\"\n\nfunc TestNetwork(t *testing.T) {\n\t_ = NetworkName(1)\n}\n",
	})
	if len(got) != 0 {
		t.Fatalf("violations = %+v, want none — the product memoizes a value it computes itself, and the test "+
			"hands nothing in", got)
	}
}

// @decision: test-wallclock-lint-ratchet
func TestATestsCallToAResetHelperTakingNothingIsTheTestsWrite(t *testing.T) {
	got := packageStateViolations(map[string]string{
		"prod.go": "package sample\n\nvar pool *int\n\nfunc reset() {\n\tpool = nil\n}\n",
		"sample_test.go": "package sample\n\nimport \"testing\"\n\n" +
			"func TestPool(t *testing.T) {\n\treset()\n}\n",
	})
	if len(got) != 1 || got[0].Kind != KindPackageState {
		t.Fatalf("violations = %+v, want one %s — a helper taking nothing writes only for its caller, so moving "+
			"the write out of the test launders nothing", got, KindPackageState)
	}
	if !strings.Contains(got[0].Detail, "pool") || !strings.Contains(got[0].Detail, "reset") ||
		!strings.Contains(got[0].Detail, "TestPool") {
		t.Errorf("the failure names neither the variable, the helper, nor the test: %s", got[0].Detail)
	}
	if got[0].Line != 6 {
		t.Errorf("violation line = %d, want the line of the call (6)", got[0].Line)
	}
}

// @decision: test-wallclock-lint-ratchet
func TestAWrapperTakingNothingAroundAResetHelperLaundersNothing(t *testing.T) {
	got := packageStateViolations(map[string]string{
		"prod.go": "package sample\n\nvar pool *int\n\nfunc reset() {\n\tpool = nil\n}\n\n" +
			"func resetForTest() {\n\treset()\n}\n",
		"sample_test.go": "package sample\n\nimport \"testing\"\n\n" +
			"func TestPool(t *testing.T) {\n\tresetForTest()\n}\n",
	})
	if len(got) != 1 || got[0].Kind != KindPackageState {
		t.Fatalf("violations = %+v, want one %s — a wrapper taking no arguments carries the write it wraps",
			got, KindPackageState)
	}
	if !strings.Contains(got[0].Detail, "pool") || !strings.Contains(got[0].Detail, "resetForTest") ||
		!strings.Contains(got[0].Detail, "TestPool") {
		t.Errorf("the failure names neither the variable, the wrapper, nor the test: %s", got[0].Detail)
	}
}
