// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package signal

import (
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @concept: terminal-tag
func BuildTerminalErrorSignal(errorClass string, errorPayload map[string]any, attempt int, retriesSoFar int, attributesDelta map[string]any, tags []string) Signal {
	return Signal{
		Type: TypePath("terminal/error/" + errorClass),
		Payload: eventpayload.New(&genv1.TerminalErrorSignalPayload{
			ErrorClass:      errorClass,
			ErrorPayload:    MustStruct(errorPayload),
			Attempt:         int32(attempt),
			RetriesSoFar:    int32(retriesSoFar),
			AttributesDelta: MustStruct(orEmptyMap(attributesDelta)),
			Tags:            tags,
		}),
	}
}

// @concept: terminal-tag
func BuildTerminalSuccessSignal(changed bool, attributesDelta map[string]any, changeSummary string, tags []string) Signal {
	return Signal{
		Type: TypePath("terminal/success"),
		Payload: eventpayload.New(&genv1.TerminalSuccessSignalPayload{
			Changed:         changed,
			AttributesDelta: MustStruct(orEmptyMap(attributesDelta)),
			ChangeSummary:   changeSummary,
			Tags:            tags,
		}),
	}
}

func MustStruct(m map[string]any) *structpb.Struct {
	if m == nil {
		return nil
	}
	out := &structpb.Struct{}
	unmarshalJSONInto(m, out)
	return out
}

func MustValue(v any) *structpb.Value {
	out := &structpb.Value{}
	unmarshalJSONInto(v, out)
	return out
}

func unmarshalJSONInto(v any, dst protoreflect.ProtoMessage) {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("signal: value of type %T is not JSON-representable: %v", v, err))
	}
	if err := protojson.Unmarshal(raw, dst); err != nil {
		panic(fmt.Sprintf("signal: value of type %T does not fit %T: %v", v, dst, err))
	}
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func PayloadSchemaForType(t TypePath) (protoreflect.MessageDescriptor, bool) {
	s := string(t)
	if s == "" {
		return nil, false
	}
	trimmed := strings.TrimSuffix(s, "*")
	switch {
	case s == "terminal/success":
		return descriptorOf(&genv1.TerminalSuccessSignalPayload{}), true
	case s == "transient/park":
		return descriptorOf(&genv1.TransientParkSignalPayload{}), true
	case s == "transient/await_async":
		return descriptorOf(&genv1.TransientAwaitAsyncSignalPayload{}), true
	case strings.HasPrefix(trimmed, "terminal/error/"):
		return descriptorOf(&genv1.TerminalErrorSignalPayload{}), true
	case strings.HasPrefix(trimmed, "transient/retry/"):
		return descriptorOf(&genv1.TransientRetrySignalPayload{}), true
	case strings.HasPrefix(trimmed, "attribute/") && strings.HasSuffix(trimmed, "/changed"):
		return descriptorOf(&genv1.AttributeChangedSignalPayload{}), true
	}
	return nil, false
}

func descriptorOf(m protoreflect.ProtoMessage) protoreflect.MessageDescriptor {
	return m.ProtoReflect().Descriptor()
}
