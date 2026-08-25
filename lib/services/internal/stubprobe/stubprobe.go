// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: conformance
// @concept: executor

package stubprobe

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/stubmode"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/execoutcome"
)

// @concept: conformance
type StubSuccess struct {
	Attributes    map[string]any
	ChangeSummary string
	ErrorClass    string
	Changed       bool
	Scratch       []byte
}

// @concept: conformance
func HasResponseOverride(attrs map[string]any) bool {
	raw, present := attrs[stubmode.ResponseOverrideAttribute]
	return present && raw != nil
}

// @concept: conformance
func SuccessDelta(attrs map[string]any) (map[string]any, error) {
	raw, present := attrs[stubmode.ResponseOverrideAttribute]
	if !present || raw == nil {
		return stubmode.ResponseDelta(), nil
	}
	override, isObject := raw.(map[string]any)
	if !isObject {
		return nil, fmt.Errorf("%s must be a JSON object, got %T", stubmode.ResponseOverrideAttribute, raw)
	}
	delta := make(map[string]any, len(override))
	for k, v := range override {
		delta[k] = v
	}
	return delta, nil
}

// @concept: conformance
func Success(spec StubSuccess) *genv1.Outcome {
	delta, err := SuccessDelta(spec.Attributes)
	if err != nil {
		return execoutcome.Errored(spec.ErrorClass, err.Error())
	}
	tags, err := Tags(spec.Attributes)
	if err != nil {
		return execoutcome.Errored(spec.ErrorClass, err.Error())
	}
	value, err := structpb.NewStruct(delta)
	if err != nil {
		return execoutcome.Errored(spec.ErrorClass,
			stubmode.ResponseOverrideAttribute+" not JSON-representable: "+err.Error())
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		AttributesDelta: value,
		Changed:         spec.Changed,
		ChangeSummary:   spec.ChangeSummary,
		Scratch:         spec.Scratch,
		Tags:            tags,
	}}}
}

// @concept: conformance
func Tags(attrs map[string]any) ([]string, error) {
	value, present := attrs[stubmode.TagsAttribute]
	if !present || value == nil {
		return nil, nil
	}
	raw, isArray := value.([]any)
	if !isArray {
		return nil, fmt.Errorf("%s must be a JSON array of strings, got %T", stubmode.TagsAttribute, value)
	}
	tags := make([]string, 0, len(raw))
	for i, v := range raw {
		s, isString := v.(string)
		if !isString {
			return nil, fmt.Errorf("%s[%d] must be a string, got %T", stubmode.TagsAttribute, i, v)
		}
		tags = append(tags, s)
	}
	return tags, nil
}

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
