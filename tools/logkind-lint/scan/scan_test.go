// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scan

import (
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

func kindsIn(t *testing.T, body string) []string {
	t.Helper()
	src := "package sample\n\nimport (\n\t\"log/slog\"\n\t\"testing\"\n)\n\nvar _ = slog.Default\n\nvar _ = testing.Verbose\n\n" + body
	return kindsInPackages(t, map[string]string{"sample.go": src})
}

func kindsInPackages(t *testing.T, sources map[string]string) []string {
	t.Helper()
	var rels []string
	for rel := range sources {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	fset := token.NewFileSet()
	var files []*file
	for _, rel := range rels {
		parsed, err := parser.ParseFile(fset, rel, sources[rel], 0)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		files = append(files, newFile(rel, fset, parsed, sources[rel]))
	}
	var out []string
	for _, v := range violationsInFiles(files) {
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

// @decision: structured-log-kind-format
func TestAKindInTheStandardsFormPasses(t *testing.T) {
	assertKinds(t, kindsIn(t, "func f() { slog.Warn(\"QUEUE.JOB.RETRIED\", \"attempt\", 2) }"))
	assertKinds(t, kindsIn(t, "func f() { slog.Info(\"QUEUE.JOB2.RETRIED4\") }"))
	assertKinds(t, kindsIn(t, "func f() { slog.Info(\"QUEUE.JOB.STATE.CHANGED\") }"))
}

// @decision: structured-log-kind-format
func TestProseInTheKindFails(t *testing.T) {
	assertKinds(t, kindsIn(t, "func f() { slog.Warn(\"job retried\", \"attempt\", 2) }"), KindMalformed)
}

// @decision: structured-log-kind-format
func TestTheLowerCaseDottedDialectFails(t *testing.T) {
	assertKinds(t, kindsIn(t, "func f() { slog.Warn(\"queue.job.retried\") }"), KindMalformed)
}

// @decision: structured-log-kind-format
func TestFewerThanThreeSegmentsFails(t *testing.T) {
	assertKinds(t, kindsIn(t, "func f() { slog.Warn(\"QUEUE.RETRIED\") }"), KindMalformed)
}

// @decision: structured-log-kind-format
func TestAnUnderscoreInsideASegmentFails(t *testing.T) {
	assertKinds(t, kindsIn(t, "func f() { slog.Warn(\"QUEUE.JOB_STATE.RETRIED\") }"), KindMalformed)
}

// @decision: structured-log-kind-format
func TestAMessageTheScanCannotReadFails(t *testing.T) {
	assertKinds(t, kindsIn(t, "func f(verb string) { slog.Warn(verb+\": retried\") }"), KindDynamic)
}

// @decision: structured-log-kind-format
func TestAForwardedCallersKindPasses(t *testing.T) {
	assertKinds(t, kindsIn(t, "func f(l *slog.Logger, msg string, args ...any) { l.Warn(msg, args...) }"))
	assertKinds(t, kindsIn(t, "func f(l *slog.Logger, r slog.Record) { l.Warn(r.Message) }"))
}

// @decision: structured-log-kind-format
func TestACallOnSomethingThatIsNotALoggerIsNoEmitSite(t *testing.T) {
	assertKinds(t, kindsIn(t, "func f(t interface{ Error(...any) }) { t.Error(\"a plain assertion message\") }"))
	assertKinds(t, kindsIn(t, "func f(err error) string { return err.Error() }"))
}

// @decision: structured-log-kind-format
func TestEveryLoggerSpellingIsAnEmitSite(t *testing.T) {
	assertKinds(t, kindsIn(t, "type deps struct{ Logger *slog.Logger }\n\nfunc f(d deps) { d.Logger.Debug(\"prose\") }"), KindMalformed)
	assertKinds(t, kindsIn(t, "func f(logger *slog.Logger) { logger.With(\"k\", 1).Error(\"prose\") }"), KindMalformed)
	assertKinds(t, kindsIn(t, "func f() { slog.Default().InfoContext(nil, \"prose\") }"), KindMalformed)
}

// @decision: structured-log-kind-format
func TestOnlyAMalformedKindIsBaselineable(t *testing.T) {
	if !(Violation{Kind: KindMalformed}).Baselineable() {
		t.Error("a malformed kind must be recordable in the baseline")
	}
	if (Violation{Kind: KindDynamic}).Baselineable() {
		t.Error("a message the scan cannot read carries no kind, so the baseline cannot record it")
	}
}

// @decision: structured-log-kind-format
func TestAKindHandedToAForwardingHelperIsCheckedAtTheCallSite(t *testing.T) {
	forwarder := "func forward(l *slog.Logger, kind string, args ...any) { l.Warn(kind, args...) }\n\n"
	assertKinds(t, kindsIn(t, forwarder+"func f(l *slog.Logger) { forward(l, \"QUEUE.JOB.RETRIED\") }"))
	assertKinds(t, kindsIn(t, forwarder+"func f(l *slog.Logger) { forward(l, \"job retried\") }"), KindMalformed)
	assertKinds(t, kindsIn(t, forwarder+"func f(l *slog.Logger, v string) { forward(l, v+\"!\") }"), KindDynamic)
}

// @decision: structured-log-kind-format
func TestAKindForwardedThroughTwoHelpersIsCheckedAtTheOutermostCallSite(t *testing.T) {
	chain := "func inner(l *slog.Logger, kind string) { l.Warn(kind) }\n\n" +
		"func outer(l *slog.Logger, kind string) { inner(l, kind) }\n\n"
	assertKinds(t, kindsIn(t, chain+"func f(l *slog.Logger) { outer(l, \"QUEUE.JOB.RETRIED\") }"))
	assertKinds(t, kindsIn(t, chain+"func f(l *slog.Logger) { outer(l, \"job retried\") }"), KindMalformed)
}

// @decision: structured-log-kind-format
func TestALocalShadowingAParameterNameIsNoPassThrough(t *testing.T) {
	assertKinds(t, kindsIn(t,
		"func f(l *slog.Logger, msg string) {\n"+
			"\tif l != nil {\n"+
			"\t\tmsg := \"job retried\"\n"+
			"\t\tl.Warn(msg)\n"+
			"\t}\n"+
			"}"), KindDynamic)
}

// @decision: structured-log-kind-format
func TestATestingHandleNamedTIsNeverALogger(t *testing.T) {
	assertKinds(t, kindsIn(t, "func f(t *testing.T) { t.Error(\"a plain assertion message\") }"))
	assertKinds(t, kindsIn(t, "type suite struct{ t *testing.T }\n\nfunc (s suite) f() { s.t.Error(\"a plain assertion message\") }"))
}

// @decision: structured-log-kind-format
func TestAFieldNamedLoggerOfANonLoggerTypeIsNoEmitSite(t *testing.T) {
	assertKinds(t, kindsIn(t, "type recorder struct{}\n\nfunc (recorder) Error(args ...any) {}\n\n"+
		"type deps struct{ logger recorder }\n\nfunc f(d deps) { d.logger.Error(\"a plain message\") }"))
}

// @decision: structured-log-kind-format
func TestAFieldOfALoggerTypeUnderAnyNameIsAnEmitSite(t *testing.T) {
	assertKinds(t, kindsIn(t, "type deps struct{ chatter *slog.Logger }\n\nfunc f(d deps) { d.chatter.Warn(\"prose\") }"),
		KindMalformed)
	assertKinds(t, kindsIn(t, "type narrow interface{ Warn(msg string, args ...any) }\n\n"+
		"type deps struct{ q narrow }\n\nfunc f(d deps) { d.q.Warn(\"prose\") }"), KindMalformed)
}

// @decision: structured-log-kind-format
func TestALoggerNameInOnePackageIsNoLoggerNameInAnother(t *testing.T) {
	assertKinds(t, kindsInPackages(t, map[string]string{
		"alpha/logging.go": "package alpha\n\nimport \"log/slog\"\n\n" +
			"type deps struct{ logger *slog.Logger }\n\nfunc f(d deps) { d.logger.Warn(\"ALPHA.JOB.RETRIED\") }\n",
		"beta/asserts.go": "package beta\n\ntype recorder struct{}\n\nfunc (recorder) Warn(args ...any) {}\n\n" +
			"type deps struct{ logger recorder }\n\nfunc f(d deps) { d.logger.Warn(\"a plain message\") }\n",
	}))
}
