// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: event-log
package eventpayload

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type Payload struct {
	fields map[string]any
}

var marshalOptions = protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}

func New(m proto.Message) Payload {
	if m == nil {
		return Payload{}
	}
	raw, err := marshalOptions.Marshal(m)
	if err != nil {
		panic(fmt.Sprintf("eventpayload.New: marshal %T: %v", m, err))
	}
	fields := map[string]any{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		panic(fmt.Sprintf("eventpayload.New: decode %T: %v", m, err))
	}
	return Payload{fields: fields}
}

func Decoded(fields map[string]any) Payload {
	return Payload{fields: fields}
}

func (p Payload) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.Map())
}

func (p *Payload) UnmarshalJSON(raw []byte) error {
	fields := map[string]any{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	p.fields = fields
	return nil
}

func (p Payload) Map() map[string]any {
	if p.fields == nil {
		return map[string]any{}
	}
	return p.fields
}
