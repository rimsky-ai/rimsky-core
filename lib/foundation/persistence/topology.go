// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"os"
)

const ProcessRoleEnv = "RIMSKY_PROCESS_ROLE"

type Topology string

const (
	TopologyUnified Topology = "unified"
	TopologySplit   Topology = "split"
)

func (t Topology) Unified() bool { return t == TopologyUnified }

func ParseTopology(processRole string) Topology {
	if processRole == string(TopologyUnified) {
		return TopologyUnified
	}
	return TopologySplit
}

func TopologyFromEnv() Topology {
	return ParseTopology(os.Getenv(ProcessRoleEnv))
}

const sqliteReplicaWarningMessage = "persistence.driver=sqlite outside the unified topology: cross-process coordination (advisory locks, transactional writer-slot holds) is safe, but every role process and every replica must share one local (non-network) database file — separate files per process/replica or a network filesystem are unsupported and undetectable from inside a process; verify your deployment provides that shared local file, or switch to postgres"

func SQLiteReplicaWarning(driver string, topology Topology) (string, bool) {
	if driver != "sqlite" || topology.Unified() {
		return "", false
	}
	return sqliteReplicaWarningMessage, true
}
