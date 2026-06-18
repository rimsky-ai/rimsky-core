// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package action

import (
	"errors"
	"fmt"
)

type Kind string

const (
	Pop          Kind = "pop"
	PopAndMove   Kind = "pop_and_move"
	PopAndDelete Kind = "pop_and_delete"
	Recycle      Kind = "recycle"
)

type Action struct {
	Kind       Kind
	MoveTarget string
}

type ValidationResult struct {
	Errors   []error
	Warnings []string
}

func (r ValidationResult) OK() bool { return len(r.Errors) == 0 }

func AllKinds() []Kind {
	return []Kind{Pop, PopAndMove, PopAndDelete, Recycle}
}

func ParseKind(s string) (Kind, error) {
	for _, k := range AllKinds() {
		if string(k) == s {
			return k, nil
		}
	}
	return "", fmt.Errorf("unknown action %q (legal: pop, pop_and_move, pop_and_delete, recycle)", s)
}

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
