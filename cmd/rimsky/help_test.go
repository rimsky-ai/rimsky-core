// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var verbTreeNodes = [][]string{
	{},
	{"version"},
	{"health"},
	{"run"},
	{"register"},
	{"deploy"},
	{"undeploy"},
	{"instantiate"},
	{"rm-instance"},
	{"ls"},
	{"logs"},
	{"watch"},
	{"audit"},
	{"template"},
	{"template", "register"},
	{"template", "lint"},
	{"template", "list"},
	{"template", "get"},
	{"template", "deploy"},
	{"template", "undeploy"},
	{"template", "rm"},
	{"tag"},
	{"tag", "create"},
	{"tag", "list"},
	{"tag", "get"},
	{"tag", "mv"},
	{"tag", "rm"},
	{"instance"},
	{"instance", "create"},
	{"instance", "list"},
	{"instance", "get"},
	{"instance", "status"},
	{"instance", "delete"},
	{"instance", "kill"},
	{"instance", "nodes"},
	{"instance", "events"},
	{"node"},
	{"node", "get"},
	{"admin"},
	{"admin", "reset"},
	{"parked"},
	{"parked", "list"},
	{"messages"},
	{"messages", "tail"},
	{"messages", "show"},
	{"asset"},
	{"asset", "list"},
	{"asset", "show"},
	{"asset", "versions"},
	{"asset", "delete"},
	{"asset", "lineage"},
	{"lineage"},
	{"lineage", "prune"},
	{"ctx"},
	{"ctx", "list"},
	{"ctx", "use"},
	{"ctx", "add"},
	{"ctx", "rm"},
	{"ctx", "current"},
	{"auth"},
	{"auth", "init"},
	{"auth", "login"},
	{"auth", "create-key"},
	{"auth", "list"},
	{"auth", "show"},
	{"auth", "revoke"},
	{"auth", "rotate"},
	{"auth", "status"},
	{"daemon"},
	{"daemon", "start"},
	{"daemon", "status"},
	{"daemon", "stop"},
	{"compose"},
	{"compose", "up"},
	{"compose", "down"},
	{"compose", "plan"},
	{"compose", "status"},
	{"compose", "run"},
	{"conformance"},
	{"conformance", "executor"},
	{"conformance", "claim-producer"},
	{"conformance", "publisher"},
	{"conformance", "validation"},
	{"conformance", "data-processing"},
	{"conformance", "lifecycle-subscriber"},
	{"conformance", "host-daemon"},
	{"conformance", "probe"},
}

func TestEveryVerbTreeNodeAnswersHelpOnStdoutAndExitsZero(t *testing.T) {
	binary := buildCLI(t)
	home := t.TempDir()

	for _, node := range verbTreeNodes {
		for _, spelling := range []string{"--help", "-h"} {
			path := strings.Join(node, " ")
			if path == "" {
				path = "(root)"
			}
			t.Run(path+" "+spelling, func(t *testing.T) {
				args := append(append([]string{}, node...), spelling)
				cmd := exec.Command(binary, args...)
				cmd.Env = append(os.Environ(),
					"HOME="+home,
					"RIMSKY_CONTEXT=",
					"RIMSKY_CONTROL_API_URL=",
					"RIMSKY_API_KEY=",
				)
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				err := cmd.Run()

				if err != nil {
					t.Fatalf("rimsky %s %s: exit %v, want 0\nstdout:\n%s\nstderr:\n%s",
						path, spelling, err, stdout.String(), stderr.String())
				}
				if !strings.Contains(stdout.String(), "rimsky") {
					t.Errorf("rimsky %s %s: stdout %q carries no usage for this node",
						path, spelling, stdout.String())
				}
				if stderr.Len() != 0 {
					t.Errorf("rimsky %s %s: wrote %q to stderr; asking for help is not an error",
						path, spelling, stderr.String())
				}
			})
		}
	}
}

func TestEveryVerbTreeNodeNamesItselfInItsOwnHelp(t *testing.T) {
	binary := buildCLI(t)
	home := t.TempDir()

	for _, node := range verbTreeNodes {
		if len(node) == 0 {
			continue
		}
		path := strings.Join(node, " ")
		t.Run(path, func(t *testing.T) {
			cmd := exec.Command(binary, append(append([]string{}, node...), "--help")...)
			cmd.Env = append(os.Environ(), "HOME="+home, "RIMSKY_CONTEXT=", "RIMSKY_CONTROL_API_URL=")
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("rimsky %s --help: %v", path, err)
			}
			if !strings.Contains(string(out), path) {
				t.Errorf("rimsky %s --help printed %q. The output never names this node, so a reader "+
					"cannot tell whose usage it is", path, string(out))
			}
		})
	}
}

func buildCLI(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "rimsky")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/rimsky: %v\n%s", err, out)
	}
	return binary
}
