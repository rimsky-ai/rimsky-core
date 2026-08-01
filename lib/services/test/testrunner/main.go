// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

const (
	testsDir      = "/tests"
	srcDir        = "/src"
	runPatternEnv = "RIMSKY_TEST_RUN"
)

func main() {
	suites := os.Args[1:]
	if len(suites) == 0 {
		fmt.Fprintf(os.Stderr,
			"usage: test-runner <suite-path> [<suite-path>...]\n"+
				"each suite path is a repo-relative test package dir (e.g. lib/services/test/instack)\n"+
				"available suites:\n  %s\n",
			strings.Join(availableSuites(), "\n  "))
		os.Exit(2)
	}
	var failed []string
	for _, suite := range suites {
		if err := runSuite(suite); err != nil {
			fmt.Fprintf(os.Stderr, "test-runner: FAIL %s: %v\n", suite, err)
			failed = append(failed, suite)
		} else {
			fmt.Printf("test-runner: PASS %s\n", suite)
		}
	}
	if len(failed) > 0 {
		fmt.Fprintf(os.Stderr, "test-runner: failed suites: %s\n", strings.Join(failed, ", "))
		os.Exit(1)
	}
	fmt.Println("test-runner: all suites passed")
}

func runSuite(suite string) error {
	bin := binaryPath(suite)
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("no compiled test binary for suite %q at %s; available suites:\n  %s",
			suite, bin, strings.Join(availableSuites(), "\n  "))
	}
	args := []string{"-test.v"}
	if pattern := os.Getenv(runPatternEnv); pattern != "" {
		args = append(args, "-test.run", pattern)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = srcDir + "/" + suite
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("test-runner: RUN %s\n", suite)
	return cmd.Run()
}

func binaryPath(suite string) string {
	return testsDir + "/" + strings.ReplaceAll(strings.Trim(suite, "/"), "/", "__") + ".test"
}

func availableSuites() []string {
	entries, err := os.ReadDir(testsDir)
	if err != nil {
		return []string{fmt.Sprintf("(cannot list %s: %v)", testsDir, err)}
	}
	var suites []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".test") {
			continue
		}
		suites = append(suites, strings.ReplaceAll(strings.TrimSuffix(name, ".test"), "__", "/"))
	}
	sort.Strings(suites)
	return suites
}
