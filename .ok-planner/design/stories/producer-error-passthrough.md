---
story: producer-error-passthrough
status: as-is
---

# Operator reads producer errors in the API response

## Story

As an operator whose store or claim producer fails during an API-triggered operation, I can read the producer's error class and message in the API response, so that I can fix the underlying problem from the response alone instead of grepping rimsky's logs.

The control-api error writer recognizes producer-error types: the error class and message the producer transmitted across the gRPC boundary are carried into the HTTP response body, under a status that distinguishes "your producer rejected this" from "rimsky broke internally" (see `decision:producer-error-passthrough`).

The error body is the one document every operator and agent reads; the diagnosis the producer already did reaches the operator instead of being discarded into a bare 500 that forces a log hunt.
