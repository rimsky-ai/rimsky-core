// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package httpnode

import (
	"context"
	"sync"
)

type firedDeadlineContext struct {
	context.Context
	fired chan struct{}
	once  sync.Once
}

func newFiredDeadlineContext(parent context.Context) (*firedDeadlineContext, func()) {
	c := &firedDeadlineContext{Context: parent, fired: make(chan struct{})}
	return c, func() { c.once.Do(func() { close(c.fired) }) }
}

func (c *firedDeadlineContext) Done() <-chan struct{} { return c.fired }

func (c *firedDeadlineContext) Err() error {
	select {
	case <-c.fired:
		return context.DeadlineExceeded
	default:
		return nil
	}
}
