---
audit: single-process-mode
artifact: decision:single-process-mode
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:37:40Z
---

# The all-in-one entrypoint running three roles in one process, with the unified marker set exactly there

Supported. Given no command, the entrypoint stamps the unified process-role marker, migrates when it owns the migration, and starts scheduler, supervisor and control-api in that order through one shared library launcher, each on its configured port; one select waits on either a signal or a role failure and drains the tracked stop functions in reverse under a single deadline. Bundled registration runs before any role starts and its failure exits the process, so a configured bundled handler that cannot be constructed aborts the boot — an unconfigured handler is skipped by a distinct sentinel and is not a failure. The bundled handlers register into the process's in-proc dispatch pool and alias table through that same registration entrypoint. Given an explicit role command the entrypoint spawns that one role as a child process instead, and strips the unified marker from the child's environment, so the marker cannot leak into a per-role process. Checked every site in the tree that sets the marker: four — the entrypoint's no-command path, the conformance runner, the compose run verb and the self-hosted run verb — which are exactly the paths that run rimsky in one shared process, and the memory blob backend's gate refuses unless that marker is set, naming those same paths in its error.
