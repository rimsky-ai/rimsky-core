// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: termination
// @story: one-shot-to-terminal
// @concept: instance

package cli

import "context"

// @concept: instance
type QuiescenceClient interface {
	ListInstanceFrames(ctx context.Context, idOrKey, state string) (*ListFramesResponse, error)
	ListInstanceMessages(ctx context.Context, idOrKey string, q ListMessagesQuery) (*ListMessagesResponse, error)
}

// @decision: termination
// @concept: instance
// @concept: frame
func InstanceQuiescence(ctx context.Context, c QuiescenceClient, idOrKey string) (bool, []FrameItem, error) {
	frames, err := c.ListInstanceFrames(ctx, idOrKey, "running")
	if err != nil {
		return false, nil, err
	}
	if len(frames.Frames) > 0 {
		return false, frames.Frames, nil
	}
	pending := true
	messages, err := c.ListInstanceMessages(ctx, idOrKey, ListMessagesQuery{Pending: &pending})
	if err != nil {
		return false, nil, err
	}
	return len(messages.Messages) == 0, nil, nil
}
