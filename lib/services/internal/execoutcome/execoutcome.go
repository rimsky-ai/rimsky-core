// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package execoutcome

import (
	"strings"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func Errored(class, msg string) *genv1.Outcome {
	payload, err := structpb.NewStruct(map[string]any{"message": strings.ToValidUTF8(msg, "�")})
	if err != nil {
		payload, _ = structpb.NewStruct(map[string]any{
			"message": "error message dropped: not representable as a struct value",
		})
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
		ErrorClass: class,
		Payload:    payload,
	}}}
}

func StubSuccess(changeSummary string) *genv1.Outcome {
	delta, _ := structpb.NewStruct(map[string]any{"stub": true})
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		AttributesDelta: delta,
		Changed:         false,
		ChangeSummary:   changeSummary,
	}}}
}
