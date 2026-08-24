---
concept: conformance
---

# Conformance

## What it is

Conformance is a runnable battery of checks that proves an independently written service matches one of rimsky's service protocols. The author points a battery at a running service. The battery drives the protocol's operations against that service and reports each check as passed or failed. Rimsky carries one battery per service protocol a service author can implement. Every battery is reachable two ways: as a command a service author in any language runs against an endpoint, and as an importable library a Go service author calls from a test against a target the test hosts itself. The batteries ship with rimsky rather than as a separate distribution.

## Purpose

Conformance lets someone outside this project prove a service is wire-compatible before deploying it. The proof needs no access to rimsky's own test code. An implementer in any language runs the battery against an endpoint, and a Go implementer reuses the same battery inside their own tests rather than writing a second one.

## Boundaries

Conformance owns the check batteries, the fixtures they drive a service with, and the runner that reports their results. It does not own rimsky's tests of rimsky's own code, which live with the code they test. It does not own the project's end-to-end scenario harness. A battery exists for each protocol a service implements.

see also: `executor`, `claim-producer`, `publisher`, `data-processing`, `validation`, `lifecycle-subscriber`
