// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: fan-out
package listarray

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type ListElement struct {
	Key     string          `json:"key"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type ListPartitionRequest struct {
	List []ListElement `json:"list"`
}

type SubScopeOutput struct {
	PartitionKey   string
	ClaimScopeData []byte
	Payload        json.RawMessage
	Address        []byte
}

func Unmarshal(b []byte) (*ListPartitionRequest, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("listarray: partition_request is empty; expected an object with a top-level \"list\" array")
	}
	var probe map[string]json.RawMessage
	probeDec := json.NewDecoder(bytes.NewReader(b))
	probeDec.DisallowUnknownFields()
	if err := probeDec.Decode(&probe); err != nil {
		return nil, fmt.Errorf("listarray: partition_request is not a JSON object: %w", err)
	}
	if _, ok := probe["list"]; !ok {
		return nil, fmt.Errorf("listarray: partition_request must be an object with a \"list\" array; got %v top-level keys without \"list\"", len(probe))
	}
	for k := range probe {
		if k != "list" {
			return nil, fmt.Errorf("listarray: partition_request has unknown top-level key %q; only \"list\" is permitted", k)
		}
	}
	req := ListPartitionRequest{}
	reqDec := json.NewDecoder(bytes.NewReader(b))
	reqDec.DisallowUnknownFields()
	if err := reqDec.Decode(&req); err != nil {
		return nil, fmt.Errorf("listarray: failed to decode list array: %w", err)
	}
	if len(req.List) == 0 {
		return nil, fmt.Errorf("listarray: \"list\" array is empty; fan-out requires at least one element (an empty list would produce zero sub-claims, leaving the parent in an undefined fan-out state)")
	}
	seen := make(map[string]int, len(req.List))
	for i, el := range req.List {
		if el.Key == "" {
			return nil, fmt.Errorf("listarray: element [%d] has an empty \"key\"; every element must declare a non-empty key", i)
		}
		if prior, dup := seen[el.Key]; dup {
			return nil, fmt.Errorf("listarray: element [%d] has key %q, which duplicates element [%d]; sibling fan-out keys must be unique so sub-claim scopes stay disjoint", i, el.Key, prior)
		}
		seen[el.Key] = i
	}
	return &req, nil
}

func ToSubScopes(req *ListPartitionRequest, claimScopeForElement func(key string) ([]byte, error)) ([]SubScopeOutput, error) {
	if req == nil {
		return nil, nil
	}
	out := make([]SubScopeOutput, 0, len(req.List))
	for _, el := range req.List {
		var scope []byte
		if claimScopeForElement != nil {
			s, err := claimScopeForElement(el.Key)
			if err != nil {
				return nil, fmt.Errorf("listarray: claim_scope for element key %q: %w", el.Key, err)
			}
			scope = s
		}
		out = append(out, SubScopeOutput{
			PartitionKey:   el.Key,
			ClaimScopeData: scope,
			Payload:        el.Payload,
			Address:        nil,
		})
	}
	return out, nil
}
