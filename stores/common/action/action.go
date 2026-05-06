// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package action defines the v2 pick-policy action vocabulary shared
// by every bundled claim-producer store. Each store imports this
// package to resolve action names and (de)serialize Action values
// from YAML.
//
// Per spec .ok-planner/specs/2026-05-06-fs-store-pick-policy-action-vocabulary-design.md §3.
package action

import (
	"errors"
	"fmt"
)

// Kind names the action's queue-entry-fate × resource-fate pair.
//
// Stores implement the subset of kinds that's meaningful for their
// underlying mechanism (fs supports all four; pg supports Pop and
// Recycle). Validators reject unsupported kinds at config-load.
type Kind string

const (
	// Pop: queue entry consumed; underlying resource kept in place.
	Pop Kind = "pop"
	// PopAndMove: queue entry consumed; resource renamed to the
	// configured target. Parameterized — Action.MoveTarget is non-empty.
	PopAndMove Kind = "pop_and_move"
	// PopAndDelete: queue entry consumed; resource destroyed.
	PopAndDelete Kind = "pop_and_delete"
	// Recycle: queue entry returned to queue tail; resource kept.
	Recycle Kind = "recycle"
)

// Action is the tagged-union result of YAML unmarshal for an
// on_commit / on_give_up field. MoveTarget is populated only when
// Kind == PopAndMove.
type Action struct {
	Kind       Kind
	MoveTarget string
}

// ValidationResult is the shape both fs-store and pg-store validators
// return. Errors fail config-load; Warnings are advisory and surfaced
// via package-level slog by the constructor.
type ValidationResult struct {
	Errors   []error
	Warnings []string
}

// OK reports whether the result has no errors.
func (r ValidationResult) OK() bool { return len(r.Errors) == 0 }

// AllKinds returns every Kind constant. Used for validator
// error-message lists and the cross-store consistency test.
func AllKinds() []Kind {
	return []Kind{Pop, PopAndMove, PopAndDelete, Recycle}
}

// ParseKind returns the Kind for s. Unknown strings produce an error
// listing the legal kinds.
func ParseKind(s string) (Kind, error) {
	for _, k := range AllKinds() {
		if string(k) == s {
			return k, nil
		}
	}
	return "", fmt.Errorf("unknown action %q (legal: pop, pop_and_move, pop_and_delete, recycle)", s)
}

// Validate checks intra-action consistency. Returns nil on success.
func (a Action) Validate() error {
	if _, err := ParseKind(string(a.Kind)); err != nil {
		return err
	}
	if a.Kind == PopAndMove && a.MoveTarget == "" {
		return errors.New("pop_and_move requires a non-empty target path")
	}
	if a.Kind != PopAndMove && a.MoveTarget != "" {
		return fmt.Errorf("action %q does not take a target; got %q", a.Kind, a.MoveTarget)
	}
	return nil
}
