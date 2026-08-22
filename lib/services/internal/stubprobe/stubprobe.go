// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: conformance
// @concept: executor

package stubprobe

import (
	"context"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/stubmode"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const defaultParkDelay = 30 * time.Second

// @concept: conformance
func Park(attrs map[string]any, incomingScratch []byte, defaultScratch string) *genv1.Outcome {
	park := &genv1.Park{
		ResumeAt: timestamppb.New(time.Now().Add(defaultParkDelay)),
		Scratch:  incomingScratch,
	}
	if len(park.Scratch) == 0 {
		park.Scratch = []byte(defaultScratch)
	}
	if raw, _ := attrs[stubmode.ParkResumeAtAttribute].(string); raw != "" {
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			park.ResumeAt = timestamppb.New(t)
		}
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Park{Park: park}}
}

var cancelProbeHTTPClient = &http.Client{Timeout: 5 * time.Second}

// @concept: conformance
func Cancel(ctx context.Context, callbackURL string) error {
	postCancelProbeSignal(callbackURL, stubmode.CancelObservedAck)
	<-ctx.Done()
	postCancelProbeSignal(callbackURL, stubmode.CancelAcknowledgedAck)
	return ctx.Err()
}

func postCancelProbeSignal(callbackURL, ackID string) {
	if callbackURL == "" {
		return
	}
	req, err := http.NewRequest(http.MethodPost, callbackURL+"/v1/callback/"+ackID,
		strings.NewReader(`{"success":{"changed":false}}`))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := cancelProbeHTTPClient.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}
