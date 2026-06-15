---
story: producer-error-passthrough
status: as-is
---

# Operator reads producer errors in the API response

## Role

As an operator whose store or claim producer fails during an API-triggered operation, I can read the producer's error class and message in the API response, so that I can fix the underlying problem from the response alone instead of grepping rimsky's logs.

## Capability

The control-api error writer recognizes producer-error types: the error class and message the producer transmitted across the gRPC boundary are carried into the HTTP response body, under a status that distinguishes "your producer rejected this" from "rimsky broke internally" (see `decision:producer-error-passthrough`).

## Business value

The error body is the one document every operator and agent reads; the diagnosis the producer already did reaches the operator instead of being discarded into a bare 500 that forces a log hunt.

## Acceptance

The operator triggers an operation that causes their producer to reject — e.g. a store rejecting an open because its backing path is misconfigured → the API response carries the producer's error class and message, under a status that distinguishes "your producer rejected this" from "rimsky broke internally."

## Falsifier

A producer failure that surfaces as a bare generic internal-server-error response with an empty or generic body — the producer's transmitted error class absent from the HTTP response between the gRPC boundary and the HTTP response.

## Proof

Demo — against a running stack with a real store, trigger a producer rejection and show the API response carrying the producer's own error class and message.
