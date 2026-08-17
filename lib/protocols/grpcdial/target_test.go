// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package grpcdial

import (
	"crypto/tls"
	"testing"
)

func TestTargetStripsEveryAcceptedScheme(t *testing.T) {
	cases := map[string]string{
		"grpc://executor:9090":   "executor:9090",
		"http://executor:9090":   "executor:9090",
		"https://executor:9090":  "executor:9090",
		"executor:9090":          "executor:9090",
		"unix:///var/run/x.sock": "unix:///var/run/x.sock",
	}
	for endpoint, want := range cases {
		if got := Target(endpoint); got != want {
			t.Errorf("Target(%q) = %q, want %q", endpoint, got, want)
		}
	}
}

func TestTransportCredentialsStayInsecureUnlessTLSIsRequired(t *testing.T) {
	for _, mode := range []string{"", "off", "optional"} {
		got := TransportCredentials(mode).Info().SecurityProtocol
		if got != "insecure" {
			t.Errorf("TransportCredentials(%q) security protocol = %q, want %q", mode, got, "insecure")
		}
	}
}

func TestTransportCredentialsSpeakTLSWhenTLSIsRequired(t *testing.T) {
	got := TransportCredentials(TLSModeRequired).Info().SecurityProtocol
	if got != "tls" {
		t.Errorf("TransportCredentials(%q) security protocol = %q, want %q", TLSModeRequired, got, "tls")
	}
}

func TestTLSClientConfigFloorsAtTLS12(t *testing.T) {
	if got := tlsClientConfig().MinVersion; got != tls.VersionTLS12 {
		t.Errorf("tlsClientConfig().MinVersion = %#x, want %#x", got, tls.VersionTLS12)
	}
}
