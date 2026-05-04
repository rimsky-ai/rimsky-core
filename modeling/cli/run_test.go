package cli_test

import (
	"context"
	"testing"
	"time"

	"github.com/fallguy/rimsky/modeling/cli"
)

func TestRunRun_Keep(t *testing.T) {
	_ = setupClitest(t)
	specPath := writeSpec(t)
	if got := cli.RunRun(context.Background(), []string{specPath}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunRun_NoKeep(t *testing.T) {
	srv := setupClitest(t)
	specPath := writeSpec(t)

	// Spawn a goroutine that marks the new instance terminal once it appears.
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if id := srv.State.MarkFirstActiveTerminated(); id != "" {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	defer func() { <-done }()

	if got := cli.RunRun(context.Background(), []string{"--no-keep", "--poll-interval", "20ms", specPath}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunLs_DefaultsToInstances(t *testing.T) {
	_ = setupClitest(t)
	if got := cli.RunLs(context.Background(), nil); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunLs_Templates(t *testing.T) {
	_ = setupClitest(t)
	if got := cli.RunLs(context.Background(), []string{"templates"}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunLs_Tags(t *testing.T) {
	_ = setupClitest(t)
	if got := cli.RunLs(context.Background(), []string{"tags"}); got != 0 {
		t.Errorf("exit %d", got)
	}
}
