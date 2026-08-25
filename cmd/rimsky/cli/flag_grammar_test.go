// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

// @decision: short-flags-single-letter
func TestShortYesConfirmsADestructiveVerbLikeTheLongFlag(t *testing.T) {
	srv := setupClitest(t)
	deployedTemplate(t, srv, "release")

	if code := cli.RunTagRm(context.Background(), []string{"-y", "release"}); code != 0 {
		t.Fatalf("tag rm -y: exit %d, want 0. -y is the short spelling of --yes", code)
	}
	for _, tag := range srv.State.ListTags() {
		if tag.Tag == "release" {
			t.Errorf("tag rm -y left the tag in place: %+v", tag)
		}
	}
}

// @decision: short-flags-single-letter
func TestShortFollowStreamsLikeTheLongFlag(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "k1", Payload: map[string]any{}})

	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = wOut
	t.Cleanup(func() { os.Stdout = saved })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- cli.RunInstanceEvents(ctx, []string{"-f", "--poll-interval", "20ms", inst.ID})
	}()

	waitForListEventsCalls(t, srv, 2)
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "k2", Payload: map[string]any{}})
	waitForListEventsCalls(t, srv, srv.ListEventsHitCount()+2)

	cancel()
	_ = wOut.Close()
	if exit := <-done; exit != 0 {
		t.Errorf("instance events -f: exit %d, want 0", exit)
	}
	buf := make([]byte, 64*1024)
	n, _ := rOut.Read(buf)
	if out := string(buf[:n]); !strings.Contains(out, "k2") {
		t.Errorf("instance events -f: output %q, want the event appended after the first poll. "+
			"-f is the short spelling of --follow", out)
	}
}

func TestTableFormatNamesTheListingAndRefusesAVerbThatRendersNoTable(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)

	var listCode int
	listing := captureStdout(t, func() {
		listCode = cli.RunInstanceList(context.Background(), []string{"-o", "table"})
	})
	if listCode != 0 {
		t.Fatalf("instance list -o table: exit %d, want 0", listCode)
	}
	if !strings.Contains(listing, "TEMPLATE") || !strings.Contains(listing, inst.ID) {
		t.Errorf("instance list -o table: stdout %q, want the listing's table", listing)
	}

	var getCode int
	complaint := captureStderr(t, func() {
		getCode = cli.RunInstanceGet(context.Background(), []string{"-o", "table", inst.ID})
	})
	if getCode != 2 {
		t.Errorf("instance get -o table: exit %d, want 2. The verb renders no table", getCode)
	}
	if !strings.Contains(complaint, "instance get") || !strings.Contains(complaint, "table") {
		t.Errorf("instance get -o table: stderr %q, want an error naming the verb and the format", complaint)
	}
}

func TestReadVerbSerializesTheSameFieldsAsYAMLAndJSON(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)

	asJSON := captureStdout(t, func() {
		if code := cli.RunInstanceGet(context.Background(), []string{"-o", "json", inst.ID}); code != 0 {
			t.Fatalf("instance get -o json: exit %d", code)
		}
	})
	asYAML := captureStdout(t, func() {
		if code := cli.RunInstanceGet(context.Background(), []string{"-o", "yaml", inst.ID}); code != 0 {
			t.Fatalf("instance get -o yaml: exit %d", code)
		}
	})

	var fromJSON, fromYAML map[string]any
	if err := json.Unmarshal([]byte(asJSON), &fromJSON); err != nil {
		t.Fatalf("instance get -o json did not emit JSON on stdout: %v (%q)", err, asJSON)
	}
	if err := yaml.Unmarshal([]byte(asYAML), &fromYAML); err != nil {
		t.Fatalf("instance get -o yaml did not emit YAML on stdout: %v (%q)", err, asYAML)
	}
	if !reflect.DeepEqual(fromJSON, fromYAML) {
		t.Errorf("-o yaml carried %#v but -o json carried %#v", fromYAML, fromJSON)
	}
}

func TestUnrecognizedOutputValueFailsInsteadOfFallingBackToHumanOutput(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)

	var code int
	complaint := captureStderr(t, func() {
		code = cli.RunInstanceGet(context.Background(), []string{"-o", "pretty", inst.ID})
	})
	if code != 2 {
		t.Errorf("instance get -o pretty: exit %d, want 2", code)
	}
	if !strings.Contains(complaint, "pretty") {
		t.Errorf("instance get -o pretty: stderr %q, want the rejected value named", complaint)
	}
}

func TestCtxCurrentSpeaksTheOneJSONSpelling(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RIMSKY_CONTEXT", "")
	cfgPath := filepath.Join(home, ".rimsky", "config.yml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := &cli.Config{
		CurrentContext: "analytics-production",
		Contexts:       map[string]cli.Context{"analytics-production": {Endpoint: "http://control.invalid"}},
	}
	if err := cli.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if code := cli.RunCtxCurrent([]string{"-o", "json"}, cfgPath); code != 0 {
			t.Fatalf("ctx current -o json: exit %d", code)
		}
	})
	var current map[string]any
	if err := json.Unmarshal([]byte(out), &current); err != nil {
		t.Fatalf("ctx current -o json did not emit JSON on stdout: %v (%q)", err, out)
	}
	if current["name"] != "analytics-production" {
		t.Errorf("ctx current -o json: got %#v, want the current context's name", current)
	}
}

func TestStreamingReadsRefuseTableBeforeTheyStream(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)

	for _, verb := range []struct {
		name string
		run  func(context.Context, []string) int
		args []string
	}{
		{"instance events", cli.RunInstanceEvents, []string{"-o", "table", inst.ID}},
		{"messages tail", cli.RunMessagesTail, []string{"-o", "table", "--instance", inst.ID}},
		{"watch", cli.RunWatch, []string{"-o", "table", inst.ID}},
	} {
		t.Run(verb.name, func(t *testing.T) {
			var code int
			complaint := captureStderr(t, func() {
				code = verb.run(context.Background(), verb.args)
			})
			if code != 2 {
				t.Errorf("%s -o table: exit %d, want 2. A stream renders no table", verb.name, code)
			}
			if !strings.Contains(complaint, "table") {
				t.Errorf("%s -o table: stderr %q, want the refused format named", verb.name, complaint)
			}
		})
	}
}

func TestStreamedYAMLParsesBackAsOneDocumentPerRecord(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "first", Payload: map[string]any{}})
	srv.State.AddEvent(cli.Event{InstanceID: inst.ID, Kind: "second", Payload: map[string]any{}})

	out := captureStdout(t, func() {
		if code := cli.RunInstanceEvents(context.Background(), []string{"-o", "yaml", inst.ID}); code != 0 {
			t.Fatalf("instance events -o yaml: exit %d", code)
		}
	})

	dec := yaml.NewDecoder(strings.NewReader(out))
	var kinds []string
	for {
		var record map[string]any
		err := dec.Decode(&record)
		if err != nil {
			break
		}
		kind, _ := record["kind"].(string)
		kinds = append(kinds, kind)
	}
	if len(kinds) != 2 {
		t.Fatalf("instance events -o yaml emitted %d document(s) from 2 events; a stream separates its "+
			"documents so it parses back. Output:\n%s", len(kinds), out)
	}
	if kinds[0] != "second" && kinds[1] != "second" {
		t.Errorf("streamed documents = %v, want both events", kinds)
	}
}
