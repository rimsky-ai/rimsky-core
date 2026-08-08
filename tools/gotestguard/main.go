// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: testing-scenario-based-e2e
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const noProgressEnv = "RIMSKY_TEST_NO_PROGRESS_SECS"

const defaultNoProgress = 20 * time.Minute

const exitInconclusive = 3

type testEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

type progress struct {
	mu       sync.Mutex
	last     time.Time
	inFlight map[string]struct{}
}

func (p *progress) touch() {
	p.mu.Lock()
	p.last = time.Now()
	p.mu.Unlock()
}

func (p *progress) since() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return time.Since(p.last)
}

func (p *progress) started(key string) {
	p.mu.Lock()
	p.inFlight[key] = struct{}{}
	p.mu.Unlock()
}

func (p *progress) finished(key string) {
	p.mu.Lock()
	delete(p.inFlight, key)
	p.mu.Unlock()
}

func (p *progress) stillRunning() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.inFlight))
	for k := range p.inFlight {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func noProgressWindow() time.Duration {
	raw := os.Getenv(noProgressEnv)
	if raw == "" {
		return defaultNoProgress
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs <= 0 {
		fmt.Fprintf(os.Stderr, "gotest-guard: %s=%q is not a positive number of seconds\n", noProgressEnv, raw)
		os.Exit(2)
	}
	return time.Duration(secs) * time.Second
}

func main() {
	window := noProgressWindow()

	args := append([]string{"test", "-json", "-timeout", "0"}, os.Args[1:]...)
	cmd := exec.Command("go", args...)
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotest-guard: stdout pipe: %v\n", err)
		os.Exit(2)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "gotest-guard: start go test: %v\n", err)
		os.Exit(2)
	}

	p := &progress{last: time.Now(), inFlight: map[string]struct{}{}}
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1<<20), 1<<24)
		for scanner.Scan() {
			p.touch()
			var ev testEvent
			if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
				fmt.Println(scanner.Text())
				continue
			}
			if ev.Output != "" {
				fmt.Print(ev.Output)
			}
			if ev.Test == "" {
				continue
			}
			key := ev.Package + "." + ev.Test
			switch ev.Action {
			case "run":
				p.started(key)
			case "pass", "fail", "skip":
				p.finished(key)
			}
		}
	}()

	killed := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-streamDone:
				return
			case <-ticker.C:
				if p.since() < window {
					continue
				}
				close(killed)
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGQUIT)
				time.Sleep(5 * time.Second)
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				return
			}
		}
	}()

	<-streamDone
	waitErr := cmd.Wait()

	select {
	case <-killed:
		reportInconclusive(p, window)
		os.Exit(exitInconclusive)
	default:
	}

	if waitErr == nil {
		os.Exit(0)
	}
	var exitErr *exec.ExitError
	if ok := asExitError(waitErr, &exitErr); ok {
		os.Exit(exitErr.ExitCode())
	}
	fmt.Fprintf(os.Stderr, "gotest-guard: go test: %v\n", waitErr)
	os.Exit(2)
}

func asExitError(err error, target **exec.ExitError) bool {
	if e, ok := err.(*exec.ExitError); ok {
		*target = e
		return true
	}
	return false
}

func reportInconclusive(p *progress, window time.Duration) {
	running := p.stillRunning()
	fmt.Println()
	fmt.Println("✖✖ RUN INCONCLUSIVE — NO TEST PROGRESS ✖✖")
	fmt.Printf("No test started, completed, or produced output for %s, so the run was killed.\n", window)
	fmt.Println("This is NOT a test failure and NOT a pass: the suite produced no verdict.")
	fmt.Println("Either a test is hung, or the machine is saturated enough that nothing advanced.")
	if len(running) == 0 {
		fmt.Println("No test was in flight when progress stopped.")
		return
	}
	fmt.Printf("In flight when progress stopped (%d):\n", len(running))
	for _, r := range running {
		fmt.Printf("  %s\n", r)
	}
}
