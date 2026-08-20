// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @story: template-lifecycle
// @story: instance-lifecycle
package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

type shortcutPair struct {
	shortcut []string
	grouped  []string
}

var shortcutVerbPairs = []shortcutPair{
	{shortcut: []string{"ls", "templates"}, grouped: []string{"template", "list"}},
	{shortcut: []string{"deploy"}, grouped: []string{"template", "deploy"}},
	{shortcut: []string{"undeploy"}, grouped: []string{"template", "undeploy"}},
	{shortcut: []string{"instantiate"}, grouped: []string{"instance", "create"}},
	{shortcut: []string{"rm-instance"}, grouped: []string{"instance", "delete"}},
}

type cliRunner struct {
	t    *testing.T
	bin  string
	home string
	base string
}

func TestDevLoopShortcutVerbsAreIndistinguishableFromTheirGroupedForms(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	stack := harness.StartAllInOneZeroConfig(ctx, t, "")
	cli := newCLIRunner(t, stack.Endpoint.BaseURL)

	for _, pair := range shortcutVerbPairs {
		cli.requireSameFlagSet(pair)
	}

	for _, pair := range shortcutVerbPairs {
		label := strings.Join(pair.shortcut, " ") + " ~ " + strings.Join(pair.grouped, " ") + ", undefined flag"
		cli.requireSameOutput(label,
			append(append([]string{}, pair.shortcut...), "--nope"),
			append(append([]string{}, pair.grouped...), "--nope"), verbatim)
	}

	humanA := cli.registerTemplate("shortcut-probe-human-a")
	humanB := cli.registerTemplate("shortcut-probe-human-b")
	jsonA := cli.registerTemplate("shortcut-probe-json-a")
	jsonB := cli.registerTemplate("shortcut-probe-json-b")

	cli.requireSameOutput("ls templates ~ template list, human",
		[]string{"ls", "templates"}, []string{"template", "list"}, verbatim)
	cli.requireSameOutput("ls templates ~ template list, json",
		[]string{"ls", "templates", "-o", "json"}, []string{"template", "list", "-o", "json"}, verbatim)
	cli.requireSameOutput("ls templates ~ template list, filtered by state",
		[]string{"ls", "templates", "--state", "registered"},
		[]string{"template", "list", "--state", "registered"}, verbatim)

	cli.requireSameOutput("deploy ~ template deploy, human",
		[]string{"deploy", humanA}, []string{"template", "deploy", humanB}, normalized)
	cli.requireSameOutput("deploy ~ template deploy, json",
		[]string{"deploy", jsonA, "-o", "json"},
		[]string{"template", "deploy", jsonB, "-o", "json"}, normalized)
	cli.requireSameOutput("deploy ~ template deploy, missing template",
		[]string{"deploy", missingTemplateRef}, []string{"template", "deploy", missingTemplateRef}, normalized)

	cli.requireSameOutput("instantiate ~ instance create, human",
		[]string{"instantiate", humanA}, []string{"instance", "create", humanB}, normalized)
	cli.requireSameOutput("instantiate ~ instance create, json",
		[]string{"instantiate", jsonA, "-o", "json"},
		[]string{"instance", "create", jsonB, "-o", "json"}, normalized)
	cli.requireSameOutput("instantiate ~ instance create, missing template",
		[]string{"instantiate", missingTemplateRef},
		[]string{"instance", "create", missingTemplateRef}, normalized)

	terminal := cli.terminalInstances(t, stack.Endpoint, 4)
	cli.requireSameOutput("rm-instance ~ instance delete, human",
		[]string{"rm-instance", terminal[0], "--yes"},
		[]string{"instance", "delete", terminal[1], "--yes"}, normalized)
	cli.requireSameOutput("rm-instance ~ instance delete, json",
		[]string{"rm-instance", terminal[2], "--yes", "-o", "json"},
		[]string{"instance", "delete", terminal[3], "--yes", "-o", "json"}, normalized)
	cli.requireSameOutput("rm-instance ~ instance delete, unknown instance",
		[]string{"rm-instance", absentInstanceA, "--yes"},
		[]string{"instance", "delete", absentInstanceB, "--yes"}, normalized)

	cli.requireSameOutput("undeploy ~ template undeploy, human",
		[]string{"undeploy", humanA}, []string{"template", "undeploy", humanB}, normalized)
	cli.requireSameOutput("undeploy ~ template undeploy, json",
		[]string{"undeploy", jsonA, "-o", "json"},
		[]string{"template", "undeploy", jsonB, "-o", "json"}, normalized)
	cli.requireSameOutput("undeploy ~ template undeploy, missing template",
		[]string{"undeploy", missingTemplateRef},
		[]string{"template", "undeploy", missingTemplateRef}, normalized)
}

const missingTemplateRef = "sha256-dead"

const absentInstanceA = "00000000-0000-0000-0000-000000000001"

const absentInstanceB = "00000000-0000-0000-0000-000000000002"

func newCLIRunner(t *testing.T, baseURL string) *cliRunner {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "rimsky")
	buildRimskyCLI(t, bin)
	home := filepath.Join(dir, "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatalf("create CLI home: %v", err)
	}
	return &cliRunner{t: t, bin: bin, home: home, base: baseURL}
}

func (c *cliRunner) run(args ...string) (string, int) {
	c.t.Helper()
	cmd := exec.Command(c.bin, args...)
	cmd.Env = append(os.Environ(),
		"HOME="+c.home,
		"RIMSKY_CONTROL_API_URL="+c.base,
		"RIMSKY_API_KEY=",
		"RIMSKY_CONTEXT=",
		"NO_COLOR=1")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err == nil {
		return out.String(), 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		c.t.Fatalf("rimsky %s: %v\noutput:\n%s", strings.Join(args, " "), err, out.String())
	}
	return out.String(), exitErr.ExitCode()
}

func (c *cliRunner) requireSameFlagSet(pair shortcutPair) {
	c.t.Helper()
	shortcut := c.flagSet(pair.shortcut)
	grouped := c.flagSet(pair.grouped)
	if len(shortcut) == 0 {
		c.t.Fatalf("%s -h reported no flags, so comparing flag sets proves nothing",
			strings.Join(pair.shortcut, " "))
	}
	if strings.Join(shortcut, "\n") != strings.Join(grouped, "\n") {
		c.t.Errorf("%s and %s report different flag sets\nshortcut:\n%s\ngrouped:\n%s",
			strings.Join(pair.shortcut, " "), strings.Join(pair.grouped, " "),
			strings.Join(shortcut, "\n"), strings.Join(grouped, "\n"))
	}
}

func (c *cliRunner) flagSet(verb []string) []string {
	c.t.Helper()
	out, _ := c.run(append(append([]string{}, verb...), "-h")...)
	flags := []string{}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  -") {
			flags = append(flags, strings.TrimSpace(line))
		}
	}
	sort.Strings(flags)
	return flags
}

type outputCanon func(string) string

func verbatim(s string) string { return s }

var (
	hashPattern      = regexp.MustCompile(`sha256-[0-9a-f]+…?`)
	uuidPattern      = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	timestampPattern = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+Z`)
)

func normalized(s string) string {
	s = hashPattern.ReplaceAllString(s, "TEMPLATE_HASH")
	s = uuidPattern.ReplaceAllString(s, "IDENTIFIER")
	return timestampPattern.ReplaceAllString(s, "TIMESTAMP")
}

func (c *cliRunner) requireSameOutput(label string, shortcut, grouped []string, canon outputCanon) {
	c.t.Helper()
	shortOut, shortCode := c.run(shortcut...)
	groupedOut, groupedCode := c.run(grouped...)
	if strings.TrimSpace(shortOut) == "" && strings.TrimSpace(groupedOut) == "" {
		c.t.Errorf("%s: both spellings printed nothing, so comparing their output proves nothing", label)
		return
	}
	if shortCode != groupedCode {
		c.t.Errorf("%s: exit codes differ — %s exited %d, %s exited %d\n%s output:\n%s\n%s output:\n%s",
			label, strings.Join(shortcut, " "), shortCode, strings.Join(grouped, " "), groupedCode,
			strings.Join(shortcut, " "), shortOut, strings.Join(grouped, " "), groupedOut)
		return
	}
	if slices.Contains(shortcut, "json") {
		c.requireJSONSuccess(label, shortcut, shortOut, shortCode)
		c.requireJSONSuccess(label, grouped, groupedOut, groupedCode)
	}
	if canon(shortOut) != canon(groupedOut) {
		c.t.Errorf("%s: output differs\n%s:\n%s\n%s:\n%s",
			label, strings.Join(shortcut, " "), canon(shortOut),
			strings.Join(grouped, " "), canon(groupedOut))
	}
}

func (c *cliRunner) requireJSONSuccess(label string, verb []string, out string, code int) {
	c.t.Helper()
	if code != 0 {
		c.t.Errorf("%s: %s exited %d, so this pair compares two failures rather than two JSON renderings:\n%s",
			label, strings.Join(verb, " "), code, out)
		return
	}
	if !json.Valid([]byte(out)) {
		c.t.Errorf("%s: %s asked for JSON and printed something else:\n%s", label, strings.Join(verb, " "), out)
	}
}

func (c *cliRunner) registerTemplate(name string) string {
	c.t.Helper()
	spec := "name: " + name + "\nversion: \"1\"\nnodes:\n  - type: work\n    kind: attribute_passthrough\n"
	path := filepath.Join(c.home, name+".yml")
	if err := os.WriteFile(path, []byte(spec), 0o644); err != nil {
		c.t.Fatalf("write template spec %s: %v", path, err)
	}
	out, code := c.run("template", "register", path, "-o", "json")
	if code != 0 {
		c.t.Fatalf("rimsky template register %s exited %d:\n%s", path, code, out)
	}
	var resp struct {
		TemplateID string `json:"template_id"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		c.t.Fatalf("decode template register output: %v:\n%s", err, out)
	}
	if resp.TemplateID == "" {
		c.t.Fatalf("template register returned no template_id:\n%s", out)
	}
	return resp.TemplateID
}

func (c *cliRunner) terminalInstances(t *testing.T, ep harness.RimskyEndpoint, want int) []string {
	t.Helper()
	ids := liveInstanceIDs(t, ep)
	if len(ids) != want {
		t.Fatalf("the instantiate spellings left %d live instances, and the delete pair needs %d "+
			"interchangeable subjects: %v", len(ids), want, ids)
	}
	for _, id := range ids {
		if out, code := c.run("instance", "kill", id, "--force"); code != 0 {
			t.Fatalf("rimsky instance kill %s exited %d:\n%s", id, code, out)
		}
	}
	awaited.Until(t, "every instance to reach a terminated state, so each delete consumes an equivalent subject",
		func() bool { return len(liveInstanceIDs(t, ep)) == 0 })
	return ids
}

func liveInstanceIDs(t *testing.T, ep harness.RimskyEndpoint) []string {
	t.Helper()
	var resp struct {
		Instances []struct {
			ID           string  `json:"id"`
			TerminatedAt *string `json:"terminated_at"`
		} `json:"instances"`
	}
	status, raw := ep.GetJSON(t, "/v1/instances", "")
	if status != http.StatusOK {
		t.Fatalf("GET /v1/instances: %d %s", status, string(raw))
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode /v1/instances: %v: %s", err, string(raw))
	}
	ids := []string{}
	for _, inst := range resp.Instances {
		if inst.TerminatedAt == nil {
			ids = append(ids, inst.ID)
		}
	}
	sort.Strings(ids)
	return ids
}
