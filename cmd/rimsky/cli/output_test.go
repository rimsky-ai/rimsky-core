// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseFormat(t *testing.T) {
	cases := []struct {
		in   string
		want Format
		err  bool
	}{
		{"", 0, true},
		{"human", FormatHuman, false},
		{"json", FormatJSON, false},
		{"JSON", FormatJSON, false},
		{"yaml", FormatYAML, false},
		{"table", FormatTable, false},
		{"text", 0, true},
		{"pretty", 0, true},
	}
	for _, c := range cases {
		got, err := ParseFormat(c.in)
		if (err != nil) != c.err {
			t.Errorf("%q: err=%v want err=%v", c.in, err, c.err)
		}
		if !c.err && got != c.want {
			t.Errorf("%q: got %v want %v", c.in, got, c.want)
		}
	}
}

func TestUnknownOutputFormatNamesTheFormatsItAccepts(t *testing.T) {
	_, err := ParseFormat("pretty")
	if err == nil {
		t.Fatal("an unrecognized -o value must fail. It must never fall back to human output")
	}
	if !strings.Contains(err.Error(), FormatNames) {
		t.Errorf("error %q does not name the accepted formats %q", err, FormatNames)
	}
}

func TestYAMLOutputCarriesTheSameFieldsAsJSON(t *testing.T) {
	value := struct {
		Ref     string   `json:"ref"`
		Removed bool     `json:"removed"`
		Tags    []string `json:"tags"`
	}{Ref: "project-alpha", Removed: true, Tags: []string{"a", "b"}}

	var asJSON, asYAML bytes.Buffer
	if err := EmitStructured(&asJSON, FormatJSON, value); err != nil {
		t.Fatal(err)
	}
	if err := EmitStructured(&asYAML, FormatYAML, value); err != nil {
		t.Fatal(err)
	}

	var fromJSON, fromYAML map[string]any
	if err := json.Unmarshal(asJSON.Bytes(), &fromJSON); err != nil {
		t.Fatalf("json output does not parse: %v", err)
	}
	if err := yaml.Unmarshal(asYAML.Bytes(), &fromYAML); err != nil {
		t.Fatalf("yaml output does not parse: %v", err)
	}
	if !reflect.DeepEqual(fromJSON, fromYAML) {
		t.Errorf("-o yaml and -o json must carry the same fields. yaml carried %#v; json carried %#v",
			fromYAML, fromJSON)
	}
}

func TestEmitJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := EmitJSON(&buf, map[string]string{"a": "b"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"a": "b"`) {
		t.Errorf("got %q", buf.String())
	}
}

func TestEmitTable(t *testing.T) {
	var buf bytes.Buffer
	EmitTable(&buf, []string{"NAME", "AGE"}, [][]string{
		{"a", "1"},
		{"bb", "22"},
	})
	out := buf.String()
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "bb") {
		t.Errorf("got %q", out)
	}
}

func TestEmitTable_EmptyRows(t *testing.T) {
	var buf bytes.Buffer
	EmitTable(&buf, []string{"X"}, nil)
	if !strings.Contains(buf.String(), "X") {
		t.Errorf("got %q", buf.String())
	}
}

func TestEmitKV(t *testing.T) {
	var buf bytes.Buffer
	EmitKV(&buf, [][2]string{{"status", "ok"}, {"endpoint", "http://x"}})
	out := buf.String()
	if !strings.Contains(out, "status:") || !strings.Contains(out, "ok") {
		t.Errorf("got %q", out)
	}
}

func TestColorEnabled_BufferIsNotTTY(t *testing.T) {
	if ColorEnabled(&bytes.Buffer{}, false) {
		t.Error("buffer should never be a TTY")
	}
}

func TestColorEnabled_FlagDisables(t *testing.T) {
	if ColorEnabled(&bytes.Buffer{}, true) {
		t.Error("flag should disable")
	}
}
