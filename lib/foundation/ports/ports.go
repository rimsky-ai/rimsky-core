// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: default-port-allocation
package ports

const CoreBlockFirst = 8080

const CoreBlockLast = 8099

const BundledBlockFirst = 9000

const BundledBlockLast = 9199

const ControlAPI = 8080

const SupervisorCallback = 8081

const HostDaemonProxyDaemonFacing = 8090

const HostDaemonProxyServiceFacing = 8091

// @decision: default-port-allocation
func CoreDefaults() map[string]int {
	return map[string]int{
		"control-api":                      ControlAPI,
		"supervisor-async-callback":        SupervisorCallback,
		"host-daemon-proxy-daemon-facing":  HostDaemonProxyDaemonFacing,
		"host-daemon-proxy-service-facing": HostDaemonProxyServiceFacing,
	}
}
