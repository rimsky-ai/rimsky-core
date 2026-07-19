// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package plumbline

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestMessageSenderNodeCanonicalNamingSymbols(t *testing.T) {
	field, ok := reflect.TypeOf(spec.TemplateNodeDef{}).FieldByName("SendsMessage")
	if !ok {
		t.Fatalf("spec.TemplateNodeDef has no SendsMessage field")
	}
	yamlTag := field.Tag.Get("yaml")
	if yamlTag != "sends_message,omitempty" {
		t.Fatalf("TemplateNodeDef.SendsMessage yaml tag = %q, want %q", yamlTag, "sends_message,omitempty")
	}
	jsonTag := field.Tag.Get("json")
	if jsonTag != "sends_message,omitempty" {
		t.Fatalf("TemplateNodeDef.SendsMessage json tag = %q, want %q", jsonTag, "sends_message,omitempty")
	}
}

func TestMessageSenderNodeRetiredEmitVocabularyAbsent(t *testing.T) {
	repoRoot := findRepoRoot(t)

	forbidden := []string{
		"emits_message",
		"EmitsMessage",
		"func emitCascadeMessage(",
		"inproc://emit_message",
	}

	allowedFiles := map[string]bool{
		filepath.Join(repoRoot, "test", "plumbline", "message_sender_vocabulary_test.go"): true,
	}

	scanRepoForForbiddenVocabulary(t, repoRoot, forbidden, nil, allowedFiles,
		"retired message-side 'emit' vocabulary %q found in %s; the repo-wide vocabulary for "+
			"cascade-driven message send is sendCascadeMessage / sends_message / "+
			"inproc://send_message -- 'emit' is never used on the message-send side")
}
