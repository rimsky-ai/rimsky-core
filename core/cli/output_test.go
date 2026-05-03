package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseFormat(t *testing.T) {
	cases := []struct {
		in   string
		want Format
		err  bool
	}{
		{"", FormatHuman, false},
		{"human", FormatHuman, false},
		{"json", FormatJSON, false},
		{"JSON", FormatJSON, false},
		{"yaml", 0, true},
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
