// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
)

// @decision: host-daemon-proxy-tls
func TestLocalLeafHolderReusesTheCAItPublishedOnAnEarlierRun(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	at := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return at }

	if _, err := newLocalLeafHolder(clock, caPath); err != nil {
		t.Fatalf("first proxy run: %v", err)
	}
	publishedRoot, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read the published root: %v", err)
	}
	pool, err := enroll.CAPoolFromPEM("local", publishedRoot)
	if err != nil {
		t.Fatalf("published root does not parse: %v", err)
	}

	restarted, err := newLocalLeafHolder(clock, caPath)
	if err != nil {
		t.Fatalf("second proxy run: %v", err)
	}
	cert, err := restarted.getCertificate(nil)
	if err != nil {
		t.Fatalf("getCertificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse the restarted proxy's leaf: %v", err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, CurrentTime: at, DNSName: enroll.ServiceServerName}); err != nil {
		t.Fatalf("a restarted proxy must serve a leaf the root every running daemon pinned verifies: %v", err)
	}

	rootAfterRestart, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read the root after the restart: %v", err)
	}
	if string(rootAfterRestart) != string(publishedRoot) {
		t.Fatal("a restarted proxy must publish the root it published before, not a fresh one")
	}
}

// @decision: host-daemon-proxy-tls
func TestLocalLeafHolderKeepsItsCAKeyPrivate(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")

	if _, err := newLocalLeafHolder(time.Now, caPath); err != nil {
		t.Fatalf("newLocalLeafHolder: %v", err)
	}

	info, err := os.Stat(localCAKeyPath(caPath))
	if err != nil {
		t.Fatalf("stat the CA key: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("the CA key is mode %v, want 0600: anyone who reads it mints a leaf every daemon trusts",
			info.Mode().Perm())
	}
	root, err := os.Stat(caPath)
	if err != nil {
		t.Fatalf("stat the CA root: %v", err)
	}
	if root.Mode().Perm() != 0o644 {
		t.Fatalf("the published root is mode %v, want 0644: every daemon reads it", root.Mode().Perm())
	}
}

// @decision: host-daemon-proxy-tls
func TestLocalLeafHolderMintsAFreshCAWhenTheKeptOneDoesNotLoad(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")

	if _, err := newLocalLeafHolder(time.Now, caPath); err != nil {
		t.Fatalf("first proxy run: %v", err)
	}
	firstRoot, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read the published root: %v", err)
	}
	if err := os.WriteFile(localCAKeyPath(caPath), []byte("not a key"), 0o600); err != nil {
		t.Fatalf("corrupt the CA key: %v", err)
	}

	if _, err := newLocalLeafHolder(time.Now, caPath); err != nil {
		t.Fatalf("second proxy run: %v", err)
	}
	secondRoot, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read the republished root: %v", err)
	}
	if string(secondRoot) == string(firstRoot) {
		t.Fatal("a CA key that does not load must be replaced, and the published root replaced with it")
	}
}

// @decision: host-daemon-proxy-tls
func TestLocalLeafHolderMintsAFreshCAWhenTheKeptPairDoesNotMatch(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	at := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return at }

	if _, err := newLocalLeafHolder(clock, caPath); err != nil {
		t.Fatalf("first proxy run: %v", err)
	}
	pinnedRoot, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read the published root: %v", err)
	}

	stranger, err := pki.GenerateCA(at)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	strangerKey, err := stranger.KeyPKCS8DER()
	if err != nil {
		t.Fatalf("KeyPKCS8DER: %v", err)
	}
	if err := os.WriteFile(localCAKeyPath(caPath), strangerKey, 0o600); err != nil {
		t.Fatalf("leave a key that does not match the published root: %v", err)
	}

	holder, err := newLocalLeafHolder(clock, caPath)
	if err != nil {
		t.Fatalf("second proxy run: %v", err)
	}
	republishedRoot, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read the republished root: %v", err)
	}
	if string(republishedRoot) == string(pinnedRoot) {
		t.Fatal("a key that does not match the kept root must produce a fresh CA and a republished root; " +
			"reusing the root serves leaves no pinned daemon can verify")
	}
	pool, err := enroll.CAPoolFromPEM("local", republishedRoot)
	if err != nil {
		t.Fatalf("republished root does not parse: %v", err)
	}
	cert, err := holder.getCertificate(nil)
	if err != nil {
		t.Fatalf("getCertificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse the leaf: %v", err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, CurrentTime: at, DNSName: enroll.ServiceServerName}); err != nil {
		t.Fatalf("the leaf must verify against the root the proxy republished: %v", err)
	}
}

// @decision: host-daemon-proxy-tls
func TestReplaceFileAtomicallyReplacesASupersededFileWholeAndLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(path, []byte("-----BEGIN CERTIFICATE-----\nsuperseded\n"), 0o644); err != nil {
		t.Fatalf("seed the superseded root: %v", err)
	}

	current := []byte("-----BEGIN CERTIFICATE-----\ncurrent\n-----END CERTIFICATE-----\n")
	if err := replaceFileAtomically(path, current, 0o644); err != nil {
		t.Fatalf("replaceFileAtomically: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the published root: %v", err)
	}
	if string(got) != string(current) {
		t.Fatalf("the published root reads %q, want the current root %q", got, current)
	}
	requireOnlyEntry(t, dir, "ca.pem")
}

// @decision: host-daemon-proxy-tls
func TestPersistLocalCADropsBothFilesWhenItCannotWriteTheRoot(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.Mkdir(caPath, 0o755); err != nil {
		t.Fatalf("seed a path the rename cannot take: %v", err)
	}

	if _, err := newLocalLeafHolder(time.Now, caPath); err == nil {
		t.Fatal("a proxy that cannot keep its CA must fail, not serve a root no daemon can read")
	}
	if _, err := os.Stat(caPath); !os.IsNotExist(err) {
		t.Fatalf("a failed persist must leave no root at the path a daemon pins; stat = %v", err)
	}
	if _, err := os.Stat(localCAKeyPath(caPath)); !os.IsNotExist(err) {
		t.Fatal("a failed persist must leave no CA key behind")
	}
}

func requireOnlyEntry(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the publish directory: %v", err)
	}
	var got []string
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	if len(got) != len(want) {
		t.Fatalf("the publish directory holds %v, want %v", got, want)
	}
	for i, name := range want {
		if got[i] != name {
			t.Fatalf("the publish directory holds %v, want %v", got, want)
		}
	}
}
