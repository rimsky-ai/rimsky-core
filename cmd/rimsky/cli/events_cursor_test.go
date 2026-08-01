// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

func TestEventsFollowOpaqueCursor(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)

	const total = 250
	base := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	for i := 0; i < total; i++ {
		srv.State.AddEvent(cli.Event{
			InstanceID: inst.ID,
			Kind:       fmt.Sprintf("k%d", i+1),
			Payload:    map[string]any{},
			OccurredAt: base.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
		})
	}

	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = wOut
	t.Cleanup(func() { os.Stdout = saved })

	collected := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 256*1024)
		tmp := make([]byte, 4096)
		for {
			n, rerr := rOut.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if rerr != nil {
				break
			}
		}
		collected <- string(buf)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- cli.RunInstanceEvents(ctx, []string{"--follow", "--poll-interval", "20ms", inst.ID})
	}()

	time.Sleep(400 * time.Millisecond)
	cancel()
	exit := <-done
	os.Stdout = saved
	_ = wOut.Close()
	out := <-collected

	if exit != 0 {
		t.Fatalf("follow loop errored (exit %d); a non-base64 cursor was rejected by the honest mock. output:\n%s", exit, out)
	}

	for i := 0; i < total; i++ {
		want := fmt.Sprintf("\t%d\tk%d\n", i+1, i+1)
		if c := strings.Count(out, want); c != 1 {
			t.Fatalf("event id=%d appeared %d times, want 1 (cross-page dedup / pagination broken)", i+1, c)
		}
	}
}
