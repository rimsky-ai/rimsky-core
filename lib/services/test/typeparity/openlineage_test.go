// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package typeparity

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	rtruntime "github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/services/subscribers/openlineage/wire"
)

func jsonTagNames(t *testing.T, v any) []string {
	t.Helper()
	typ := reflect.TypeOf(v)
	out := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func assertSameTagNames(t *testing.T, kind string, writerSide, subscriberSide any) {
	t.Helper()
	writerTags := jsonTagNames(t, writerSide)
	subscriberTags := jsonTagNames(t, subscriberSide)
	if !reflect.DeepEqual(writerTags, subscriberTags) {
		t.Errorf("%s: lib/runtime writer-side json tags %v diverged from "+
			"lib/services/subscribers/openlineage/wire subscriber-side json tags %v; "+
			"the openlineage subscriber hand-mirrors these fields across the consumption-side "+
			"isolation boundary (it may not import lib/runtime), so a coordinated field change "+
			"on one side must be mirrored on the other in the same change",
			kind, writerTags, subscriberTags)
	}
}

func TestOpenlineageLeafRunRecordFieldsMatchLineageWriter(t *testing.T) {
	assertSameTagNames(t, "LeafRunRecord", rtruntime.LeafRunRecord{}, wire.LeafRunRecord{})
}

func TestOpenlineageClaimTerminalRecordFieldsMatchLineageWriter(t *testing.T) {
	assertSameTagNames(t, "ClaimTerminalRecord", rtruntime.ClaimTerminalRecord{}, wire.ClaimTerminalRecord{})
}
