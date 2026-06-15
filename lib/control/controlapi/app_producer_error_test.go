// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// app_producer_error_test.go — unit coverage for writeError's
// producer-error leg: a *peer.ProducerCallError (the typed translation
// every remote-producer client returns) must cross the HTTP boundary
// carrying the producer's transmitted error class and message, under a
// status distinguishing producer failure (502) / producer rejection of
// operator input (422) from rimsky-internal errors (500). Pure
// unit tests — no Postgres container.
package controlapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

// classedGRPCError builds the wire shape a real store transmits: a gRPC
// status with a google.rpc.ErrorInfo detail whose Reason is the rimsky
// error_class.
func classedGRPCError(t *testing.T, code codes.Code, class, msg string) error {
	t.Helper()
	st := status.New(code, msg)
	withInfo, err := st.WithDetails(&errdetails.ErrorInfo{
		Reason: class,
		Domain: "rimsky.store-test",
	})
	require.NoError(t, err)
	return withInfo.Err()
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

func TestWriteErrorProducerFailureIs502WithClassAndMessage(t *testing.T) {
	t.Parallel()
	grpcErr := classedGRPCError(t, codes.Internal,
		"fs/root_unavailable",
		"filesystem store: Release: configured root \"/workspace/data\" is not accessible")
	pcErr := peer.NewProducerCallError("docs-store", "Release", grpcErr)

	rec := httptest.NewRecorder()
	writeError(rec, pcErr)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	body := decodeBody(t, rec)
	require.Equal(t, "docs-store", body["producer_name"])
	require.Equal(t, "fs/root_unavailable", body["error_class"])
	require.Equal(t,
		"filesystem store: Release: configured root \"/workspace/data\" is not accessible",
		body["message"])
	require.Contains(t, body["error"], "docs-store")
	require.Contains(t, body["error"], "Release")
}

func TestWriteErrorProducerRejectionIs422(t *testing.T) {
	t.Parallel()
	for _, code := range []codes.Code{codes.InvalidArgument, codes.FailedPrecondition, codes.OutOfRange} {
		grpcErr := classedGRPCError(t, code, "pg/swap_failed", "store rejected the operator request")
		pcErr := peer.NewProducerCallError("analytics-store", "Commit", grpcErr)

		rec := httptest.NewRecorder()
		writeError(rec, pcErr)

		require.Equalf(t, http.StatusUnprocessableEntity, rec.Code, "gRPC code %s", code)
		body := decodeBody(t, rec)
		require.Equal(t, "analytics-store", body["producer_name"])
		require.Equal(t, "pg/swap_failed", body["error_class"])
		require.Equal(t, "store rejected the operator request", body["message"])
	}
}

// TestWriteErrorProducerUnclassedFailure — a producer fault transmitted
// with NO ErrorInfo class still gets the 502 + producer_name + message
// treatment; error_class is "" (the producer named no class — nothing
// was discarded).
func TestWriteErrorProducerUnclassedFailure(t *testing.T) {
	t.Parallel()
	grpcErr := status.Error(codes.Unavailable, "connection refused")
	pcErr := peer.NewProducerCallError("docs-store", "Open", grpcErr)

	rec := httptest.NewRecorder()
	writeError(rec, pcErr)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	body := decodeBody(t, rec)
	require.Equal(t, "docs-store", body["producer_name"])
	require.Equal(t, "", body["error_class"])
	require.Equal(t, "connection refused", body["message"])
}

// TestWriteErrorRecognizesWrappedProducerError — a wrapped
// ProducerCallError (handlers often add context via %w) is still
// recognized through errors.As.
func TestWriteErrorRecognizesWrappedProducerError(t *testing.T) {
	t.Parallel()
	grpcErr := classedGRPCError(t, codes.Internal, "fs/root_unavailable", "root gone")
	wrapped := fmt.Errorf("releasing asset: %w",
		peer.NewProducerCallError("docs-store", "Release", grpcErr))

	rec := httptest.NewRecorder()
	writeError(rec, wrapped)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	body := decodeBody(t, rec)
	require.Equal(t, "docs-store", body["producer_name"])
	require.Equal(t, "fs/root_unavailable", body["error_class"])
}

// TestWriteErrorInternalErrorsUnchanged — internal errors keep the
// pre-existing classification: a plain error stays a bare 500 with the
// {"error": ...} envelope and no producer fields. Producer-error
// handling must not reclassify them.
func TestWriteErrorInternalErrorsUnchanged(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeError(rec, errors.New("some internal failure"))

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	body := decodeBody(t, rec)
	require.Equal(t, "some internal failure", body["error"])
	_, hasProducer := body["producer_name"]
	require.False(t, hasProducer)
	_, hasClass := body["error_class"]
	require.False(t, hasClass)
}
