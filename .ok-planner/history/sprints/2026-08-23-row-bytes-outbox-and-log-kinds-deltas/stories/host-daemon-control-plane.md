---
story: host-daemon-control-plane
---

# Operator manages daemon lifecycle via CLI

## Story

As an operator running rimsky-dispatched workflows on a dev machine, I can start the host-daemon locally, check its connection status, and stop it cleanly (children reaped) through the host-daemon control-plane CLI surface, so that I manage the daemon's lifecycle from the same CLI that drives the rimsky stack.
