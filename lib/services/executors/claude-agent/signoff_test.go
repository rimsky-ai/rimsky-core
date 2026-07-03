// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"reflect"
	"strings"
	"testing"
)

type testSigner struct {
	publicKeyPEM string
	privateKey   ed25519.PrivateKey
}

func makeTestSigner(t *testing.T) *testSigner {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	spki, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	pemText := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: spki}))
	return &testSigner{publicKeyPEM: pemText, privateKey: priv}
}

func (s *testSigner) sign(t *testing.T, dispatchID string, value any) string {
	t.Helper()
	msg, err := BuildSignoffMessage(dispatchID, value)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(s.privateKey, msg))
}

func TestVerifyRequiredSignoffsAcceptsValidPerPathSignature(t *testing.T) {
	signer := makeTestSigner(t)
	delta := map[string]any{"endpoints": []any{map[string]any{"url": "x"}}}
	sig := signer.sign(t, "disp-1", delta["endpoints"])
	res := VerifyRequiredSignoffs(
		[]RequiredSignoff{{PublicKey: signer.publicKeyPEM, Path: "endpoints"}},
		delta, "disp-1", []string{sig},
	)
	if !res.OK || len(res.Unmet) != 0 {
		t.Fatalf("expected ok, got %+v", res)
	}
}

func TestVerifyRequiredSignoffsRejectsEmptySignatureBag(t *testing.T) {
	signer := makeTestSigner(t)
	delta := map[string]any{"endpoints": []any{map[string]any{"url": "x"}}}
	res := VerifyRequiredSignoffs(
		[]RequiredSignoff{{PublicKey: signer.publicKeyPEM, Path: "endpoints"}},
		delta, "disp-1", nil,
	)
	want := []UnmetSignoff{{Path: "endpoints", Reason: "missing"}}
	if res.OK || !reflect.DeepEqual(res.Unmet, want) {
		t.Fatalf("expected missing, got %+v", res)
	}
}

func TestVerifyRequiredSignoffsRejectsSignatureOverDifferentValue(t *testing.T) {
	signer := makeTestSigner(t)
	delta := map[string]any{"endpoints": []any{map[string]any{"url": "x"}}}
	sig := signer.sign(t, "disp-1", []any{map[string]any{"url": "WRONG"}})
	res := VerifyRequiredSignoffs(
		[]RequiredSignoff{{PublicKey: signer.publicKeyPEM, Path: "endpoints"}},
		delta, "disp-1", []string{sig},
	)
	want := []UnmetSignoff{{Path: "endpoints", Reason: "invalid"}}
	if res.OK || !reflect.DeepEqual(res.Unmet, want) {
		t.Fatalf("expected invalid, got %+v", res)
	}
}

func TestVerifyRequiredSignoffsRejectsSignatureFromDifferentKey(t *testing.T) {
	required := makeTestSigner(t)
	attacker := makeTestSigner(t)
	delta := map[string]any{"endpoints": []any{map[string]any{"url": "x"}}}
	sig := attacker.sign(t, "disp-1", delta["endpoints"])
	res := VerifyRequiredSignoffs(
		[]RequiredSignoff{{PublicKey: required.publicKeyPEM, Path: "endpoints"}},
		delta, "disp-1", []string{sig},
	)
	want := []UnmetSignoff{{Path: "endpoints", Reason: "invalid"}}
	if res.OK || !reflect.DeepEqual(res.Unmet, want) {
		t.Fatalf("expected invalid, got %+v", res)
	}
}

func TestVerifyRequiredSignoffsRejectsSignatureBoundToDifferentDispatchID(t *testing.T) {
	signer := makeTestSigner(t)
	delta := map[string]any{"endpoints": []any{map[string]any{"url": "x"}}}
	sig := signer.sign(t, "other-dispatch", delta["endpoints"])
	res := VerifyRequiredSignoffs(
		[]RequiredSignoff{{PublicKey: signer.publicKeyPEM, Path: "endpoints"}},
		delta, "disp-1", []string{sig},
	)
	want := []UnmetSignoff{{Path: "endpoints", Reason: "invalid"}}
	if res.OK || !reflect.DeepEqual(res.Unmet, want) {
		t.Fatalf("expected invalid, got %+v", res)
	}
}

func TestVerifyRequiredSignoffsRequiresEveryKeyForMultiplePaths(t *testing.T) {
	a := makeTestSigner(t)
	b := makeTestSigner(t)
	delta := map[string]any{
		"endpoints": []any{map[string]any{"url": "x"}},
		"summary":   "done",
	}
	sigA := a.sign(t, "disp-1", delta["endpoints"])
	sigB := b.sign(t, "disp-1", delta["summary"])
	required := []RequiredSignoff{
		{PublicKey: a.publicKeyPEM, Path: "endpoints"},
		{PublicKey: b.publicKeyPEM, Path: "summary"},
	}

	both := VerifyRequiredSignoffs(required, delta, "disp-1", []string{sigA, sigB})
	if !both.OK || len(both.Unmet) != 0 {
		t.Fatalf("expected both satisfied, got %+v", both)
	}

	onlyOne := VerifyRequiredSignoffs(required, delta, "disp-1", []string{sigA})
	want := []UnmetSignoff{{Path: "summary", Reason: "invalid"}}
	if onlyOne.OK || !reflect.DeepEqual(onlyOne.Unmet, want) {
		t.Fatalf("expected summary invalid, got %+v", onlyOne)
	}
}

func TestVerifyRequiredSignoffsIsolatesPaths(t *testing.T) {
	signer := makeTestSigner(t)
	delta := map[string]any{
		"endpoints": []any{map[string]any{"url": "x"}},
		"summary":   "done",
	}
	sigEndpoints := signer.sign(t, "disp-1", delta["endpoints"])
	res := VerifyRequiredSignoffs(
		[]RequiredSignoff{{PublicKey: signer.publicKeyPEM, Path: "summary"}},
		delta, "disp-1", []string{sigEndpoints},
	)
	want := []UnmetSignoff{{Path: "summary", Reason: "invalid"}}
	if res.OK || !reflect.DeepEqual(res.Unmet, want) {
		t.Fatalf("expected invalid, got %+v", res)
	}
}

func TestVerifyRequiredSignoffsAcceptsKeyOrderVariants(t *testing.T) {
	signer := makeTestSigner(t)
	sig := signer.sign(t, "disp-1", map[string]any{"a": 1, "b": 2})
	delta := map[string]any{"payload": map[string]any{"b": 2, "a": 1}}
	res := VerifyRequiredSignoffs(
		[]RequiredSignoff{{PublicKey: signer.publicKeyPEM, Path: "payload"}},
		delta, "disp-1", []string{sig},
	)
	if !res.OK || len(res.Unmet) != 0 {
		t.Fatalf("expected ok across key order, got %+v", res)
	}
}

func TestVerifyRequiredSignoffsSupportsRootSignature(t *testing.T) {
	signer := makeTestSigner(t)
	delta := map[string]any{
		"endpoints": []any{map[string]any{"url": "x"}},
		"summary":   "done",
	}
	sig := signer.sign(t, "disp-1", delta)
	res := VerifyRequiredSignoffs(
		[]RequiredSignoff{{PublicKey: signer.publicKeyPEM}},
		delta, "disp-1", []string{sig},
	)
	if !res.OK {
		t.Fatalf("expected root signature accepted, got %+v", res)
	}

	missing := VerifyRequiredSignoffs(
		[]RequiredSignoff{{PublicKey: signer.publicKeyPEM}},
		delta, "disp-1", nil,
	)
	want := []UnmetSignoff{{Path: "$", Reason: "missing"}}
	if missing.OK || !reflect.DeepEqual(missing.Unmet, want) {
		t.Fatalf("expected root missing, got %+v", missing)
	}
}

type wireCompatVector struct {
	DispatchID   string `json:"dispatch_id"`
	ValueJSON    string `json:"value_json"`
	Canonical    string `json:"canonical"`
	SignatureB64 string `json:"signature_b64"`
}

type wireCompatFixture struct {
	PublicKeyPEM string           `json:"public_key_pem"`
	ObjectValue  wireCompatVector `json:"object_value"`
	ScalarValue  wireCompatVector `json:"scalar_value"`
}

func loadWireCompatFixture(t *testing.T) wireCompatFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/signoff-wire-compat.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture wireCompatFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func assertWireCompat(t *testing.T, publicKeyPEM string, vector wireCompatVector) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(vector.ValueJSON))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		t.Fatal(err)
	}

	msg, err := BuildSignoffMessage(vector.DispatchID, value)
	if err != nil {
		t.Fatal(err)
	}
	wantMsg := SignoffDomain + "\n" + vector.DispatchID + "\n" + vector.Canonical
	if string(msg) != wantMsg {
		t.Fatalf("canonical message mismatch:\n got: %q\nwant: %q", msg, wantMsg)
	}

	delta := map[string]any{"payload": value}
	res := VerifyRequiredSignoffs(
		[]RequiredSignoff{{PublicKey: publicKeyPEM, Path: "payload"}},
		delta, vector.DispatchID, []string{vector.SignatureB64},
	)
	if !res.OK {
		t.Fatalf("TypeScript-minted signature rejected by Go verifier: %+v", res)
	}
}

func TestVerifyRequiredSignoffsWireCompatObjectValue(t *testing.T) {
	fixture := loadWireCompatFixture(t)
	assertWireCompat(t, fixture.PublicKeyPEM, fixture.ObjectValue)
}

func TestVerifyRequiredSignoffsWireCompatScalarValue(t *testing.T) {
	fixture := loadWireCompatFixture(t)
	assertWireCompat(t, fixture.PublicKeyPEM, fixture.ScalarValue)
}
