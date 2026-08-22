// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: message-schema
package node

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v5"

	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
)

type MessageBodySchemaViolation struct {
	Type string
	Err  error
}

func (e *MessageBodySchemaViolation) Error() string {
	return fmt.Sprintf("message body for type %q violates its declared body_schema: %v", e.Type, e.Err)
}

func (e *MessageBodySchemaViolation) Unwrap() error { return e.Err }

// @concept: message-schema
func ValidateMessageBody(spec *TemplateSpec, messageType string, payload json.RawMessage) error {
	if spec == nil {
		return nil
	}
	var schema []byte
	found := false
	for _, m := range spec.Messages {
		if m.Type == messageType {
			schema = m.BodySchema
			found = true
			break
		}
	}
	if !found || len(schema) == 0 {
		return nil
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("message-body.json", bytes.NewReader(schema)); err != nil {
		return fmt.Errorf("ValidateMessageBody: body_schema for %q is not valid JSON: %w", messageType, err)
	}
	compiled, err := compiler.Compile("message-body.json")
	if err != nil {
		return fmt.Errorf("ValidateMessageBody: body_schema for %q does not compile: %w", messageType, err)
	}
	body := payload
	if len(body) == 0 {
		body = []byte(`{}`)
	}
	var normalized any
	if err := json.Unmarshal(body, &normalized); err != nil {
		return &MessageBodySchemaViolation{Type: messageType, Err: fmt.Errorf("payload is not valid JSON: %w", err)}
	}
	// @concept: inertness
	if err := compiled.Validate(normalized); err != nil {
		return &MessageBodySchemaViolation{Type: messageType, Err: attributes.ValueFreeValidationError(err)}
	}
	return nil
}
