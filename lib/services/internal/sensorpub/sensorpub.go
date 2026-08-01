// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sensorpub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/publisherkit"
)

func PostMessage(
	ctx context.Context,
	client *http.Client,
	logger publisherkit.Logger,
	rimskyEndpoint string,
	senderName string,
	messageType string,
	instanceID string,
	subscriptionID string,
	payload map[string]any,
	idempotencyKey string,
) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	envelope := map[string]any{
		"type":                      messageType,
		"payload":                   json.RawMessage(payloadBytes),
		"sender":                    senderName,
		"publisher_subscription_id": subscriptionID,
	}
	raw, err := publisherkit.MarshalEnvelope(envelope)
	if err != nil {
		return err
	}
	messageURL := strings.TrimRight(rimskyEndpoint, "/") + "/v1/instances/" + url.PathEscape(instanceID) + "/messages"
	res := publisherkit.Send(ctx, client, logger, nil, publisherkit.Request{
		URL:            messageURL,
		Envelope:       raw,
		IdempotencyKey: idempotencyKey,
		PublisherName:  senderName,
		SubscriptionID: subscriptionID,
	})
	return res.Err
}
