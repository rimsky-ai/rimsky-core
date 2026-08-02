---
story: producer-error-passthrough
---

# Operator reads producer errors in the API response

## Story

As an operator whose store or claim producer fails during an API-triggered operation, I can read the producer's error class and message in the API response, so that I can fix the underlying problem from the response alone instead of grepping rimsky's logs.
