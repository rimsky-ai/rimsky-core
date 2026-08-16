---
audit: image-set-bundled-services
artifact: decision:image-set-bundled-services
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:25Z
checked: 11
unaccounted: 0
---

# Whether every bundled service ships as its own image, one apiece

Supported, and the mapping is exactly one to one. Enumerating the bundled services from the tree gives 11 shipped entrypoints — two claim producers, four sensors, one subscriber, and four executors — each with its own co-located image definition, and the build target builds precisely those 11 under per-service image names; the release push target pushes the same 11. No shipped service lacks an image and no image covers more than one service, so a deployment pulls only what it runs. A fitness test asserts the target still builds from co-located definitions and that every definition it references exists.
