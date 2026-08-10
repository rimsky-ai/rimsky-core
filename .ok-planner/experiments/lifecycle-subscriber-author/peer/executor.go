// The executor half of the lifecycle peer, reused verbatim from the
// permissive-peer-build experiment's peer so the service is a real rimsky
// peer that a template can name.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const (
	refusedClass = "third-party/refused"
	brokenClass  = "third-party/broken"
	servedTag    = "third-party.served"
	refusedTag   = "third-party.refused"
)

var attributesSchema = []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "outcome": {"type": "string", "default": "ok"},
    "echo": {"type": "string", "default": "hello"},
    "sleep_ms": {"type": "integer", "default": 0},
    "emit_tag": {"type": "string", "default": ""},
    "served_by": {"type": "string", "readOnly": true}
  }
}`)

type executor struct {
	genv1.UnimplementedExecutorServer
	label string
}

func (e executor) Execute(_ context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	attrs := req.GetAttributes().AsMap()
	outcome, _ := attrs["outcome"].(string)
	log.Printf("execute node=%s dispatch=%s outcome=%v", req.GetNodeType(), req.GetDispatchId(), outcome)

	if ms, ok := attrs["sleep_ms"].(float64); ok && ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}

	var tags []string
	if t, _ := attrs["emit_tag"].(string); t != "" {
		tags = []string{t}
	}

	switch outcome {
	case "fail":
		payload, _ := structpb.NewStruct(map[string]any{
			"reason": "the node asked this peer to refuse",
			"peer":   e.label,
		})
		if tags == nil {
			tags = []string{refusedTag}
		}
		return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
			ErrorClass: refusedClass,
			Payload:    payload,
			Tags:       tags,
		}}}, nil
	case "broken":
		payload, _ := structpb.NewStruct(map[string]any{"peer": e.label})
		return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
			ErrorClass: brokenClass,
			Payload:    payload,
		}}}, nil
	case "park":
		resume := time.Now().Add(24 * time.Hour)
		return &genv1.Outcome{Outcome: &genv1.Outcome_Park{Park: &genv1.Park{
			ResumeAt: timestamppb.New(resume),
			Scratch:  req.GetScratch(),
		}}}, nil
	}

	delta, err := structpb.NewStruct(map[string]any{
		"served_by": e.label,
		"echo":      fmt.Sprint(attrs["echo"]),
	})
	if err != nil {
		return nil, err
	}
	if tags == nil {
		tags = []string{servedTag}
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		Changed:         true,
		ChangeSummary:   "served by " + e.label,
		AttributesDelta: delta,
		Scratch:         req.GetScratch(),
		Tags:            tags,
	}}}, nil
}

type observability struct {
	genv1.UnimplementedExecutorObservabilityServer
}

func (observability) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		ExpectedAttributesSchema: attributesSchema,
		DeclaredErrorClasses:     []string{refusedClass, brokenClass},
		DeclaredTags:             []string{servedTag, refusedTag},
	}, nil
}

