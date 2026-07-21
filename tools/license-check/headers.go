// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"
)

const (
	markerCopyright       = "Copyright ©"
	markerLicensedUnder   = "Licensed under"
	markerApache          = "Apache License"
	markerDualAGPL        = "Dual-licensed under AGPL"
	markerAGPLFilePointer = "LICENSE.agpl"
	markerSPDXApache      = "SPDX-License-Identifier: Apache-2.0"
	markerSPDXDualAGPL    = "SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial"
)

const apacheHeaderGo = `// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.
`

const agplHeaderGo = `// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.
`

const apacheHeaderTS = `// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.
`

const agplHeaderTS = `// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.
`

const apacheHeaderProto = `// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
`

const agplHeaderProto = `// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.
`

const apacheHeaderSQL = `-- Copyright © 2026 Fall Guy Consulting.
-- Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
-- repo root, or http://www.apache.org/licenses/LICENSE-2.0.
`

const agplHeaderSQL = `-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
`

const apacheHeaderShell = `# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.
`

const agplHeaderShell = `# Copyright © 2026 Fall Guy Consulting.
# Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
# license. See LICENSE.agpl and COPYRIGHT at the repo root.
`

type violation struct {
	path    string
	message string
}

func verifyHeaders(files []fileEntry) []violation {
	var out []violation
	for _, f := range files {
		if f.classification == classUnknown {
			out = append(out, violation{
				path:    f.relPath,
				message: "unclassified source file (add its directory to licensing.yml)",
			})
			continue
		}
		hasHeader, isApache, isAGPL, err := detectHeader(f.absPath)
		if err != nil {
			out = append(out, violation{path: f.relPath, message: err.Error()})
			continue
		}
		if !hasHeader {
			out = append(out, violation{path: f.relPath, message: "missing license header"})
			continue
		}
		if isApache && isAGPL {
			out = append(out, violation{path: f.relPath, message: "contradictory license markers (both Apache and AGPL) — run `make license-stamp`"})
			continue
		}
		switch f.classification {
		case classApache:
			if !isApache {
				out = append(out, violation{path: f.relPath, message: "expected Apache header but found AGPL or unknown"})
			}
		case classAGPL:
			if !isAGPL {
				out = append(out, violation{path: f.relPath, message: "expected AGPL dual-license header but found Apache or unknown"})
			}
		}
	}
	return out
}

func detectHeader(path string) (hasHeader, isApache, isAGPL bool, err error) {
	fh, err := os.Open(path)
	if err != nil {
		return false, false, false, err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	var sawCopyright, sawLicensedUnder, sawDualAGPLPhrase, sawAGPLFilePointer, sawSPDXApache, sawSPDXAGPL bool
	for i := 0; i < 10 && sc.Scan(); i++ {
		line := sc.Text()
		if strings.Contains(line, markerCopyright) {
			sawCopyright = true
		}
		if strings.Contains(line, markerLicensedUnder) {
			sawLicensedUnder = true
		}
		if strings.Contains(line, markerApache) {
			isApache = true
		}
		if strings.Contains(line, markerDualAGPL) {
			sawDualAGPLPhrase = true
		}
		if strings.Contains(line, markerAGPLFilePointer) {
			sawAGPLFilePointer = true
		}
		if strings.Contains(line, markerSPDXApache) {
			isApache = true
			sawSPDXApache = true
		}
		if strings.Contains(line, markerSPDXDualAGPL) {
			sawSPDXAGPL = true
		}
	}
	if err := sc.Err(); err != nil {
		return false, false, false, err
	}
	isAGPL = sawSPDXAGPL || (sawDualAGPLPhrase && sawAGPLFilePointer)
	hasProseHeader := sawCopyright && (sawLicensedUnder || sawDualAGPLPhrase)
	hasSPDXHeader := sawSPDXApache || sawSPDXAGPL
	hasHeader = hasProseHeader || hasSPDXHeader
	return hasHeader, isApache, isAGPL, nil
}

func stampHeaders(files []fileEntry) (int, error) {
	stamped := 0
	for _, f := range files {
		if f.classification == classUnknown {
			return stamped, fmt.Errorf("%s: unclassified source file", f.relPath)
		}
		hasHeader, isApache, isAGPL, err := detectHeader(f.absPath)
		if err != nil {
			return stamped, fmt.Errorf("%s: %w", f.relPath, err)
		}
		mixed := isApache && isAGPL
		correct := !mixed && ((f.classification == classApache && isApache) ||
			(f.classification == classAGPL && isAGPL))
		if hasHeader && correct {
			continue
		}
		if err := stampOne(f, hasHeader); err != nil {
			return stamped, fmt.Errorf("%s: %w", f.relPath, err)
		}
		stamped++
	}
	return stamped, nil
}

func stampOne(f fileEntry, replaceExisting bool) error {
	body, err := os.ReadFile(f.absPath)
	if err != nil {
		return err
	}
	if replaceExisting {
		body = stripLeadingHeader(body, f.kind)
	}
	header := headerFor(f)
	newContents := splice(body, header, f.kind)
	return os.WriteFile(f.absPath, newContents, 0o644)
}

var licenseHeaderMarkers = []string{
	"Copyright ©",
	"Licensed under",
	"Dual-licensed under",
	"LICENSE",
	"apache.org/licenses",
	"repo root",
	"SPDX-License-Identifier",
}

func commentPrefixFor(kind sourceKind) string {
	switch kind {
	case kindSQL:
		return "--"
	case kindShell:
		return "#"
	default:
		return "//"
	}
}

func stripLeadingHeader(body []byte, kind sourceKind) []byte {
	prefix := commentPrefixFor(kind)
	lines := strings.SplitAfter(string(body), "\n")
	i := 0
	if i < len(lines) {
		first := strings.TrimSpace(lines[i])
		if (kind == kindGo && strings.HasPrefix(first, "// Code generated")) ||
			strings.HasPrefix(first, "#!") {
			i++
		}
	}
	preambleEnd := i
	lastHeaderIdx := -1
	for i < len(lines) {
		if isHeaderLine(lines[i], prefix) {
			lastHeaderIdx = i
			i++
			continue
		}
		if strings.TrimSpace(lines[i]) == "" && i+1 < len(lines) && isHeaderLine(lines[i+1], prefix) {
			i++
			continue
		}
		break
	}
	if lastHeaderIdx < preambleEnd {
		return body
	}
	if i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	var b strings.Builder
	for j := 0; j < preambleEnd; j++ {
		b.WriteString(lines[j])
	}
	for j := i; j < len(lines); j++ {
		b.WriteString(lines[j])
	}
	return []byte(b.String())
}

func isHeaderLine(line, commentPrefix string) bool {
	if !strings.HasPrefix(strings.TrimSpace(line), commentPrefix) {
		return false
	}
	for _, m := range licenseHeaderMarkers {
		if strings.Contains(line, m) {
			return true
		}
	}
	return false
}

func headerFor(f fileEntry) string {
	switch f.kind {
	case kindGo:
		if f.classification == classAGPL {
			return agplHeaderGo
		}
		return apacheHeaderGo
	case kindTS:
		if f.classification == classAGPL {
			return agplHeaderTS
		}
		return apacheHeaderTS
	case kindProto:
		if f.classification == classAGPL {
			return agplHeaderProto
		}
		return apacheHeaderProto
	case kindSQL:
		if f.classification == classAGPL {
			return agplHeaderSQL
		}
		return apacheHeaderSQL
	case kindShell:
		if f.classification == classAGPL {
			return agplHeaderShell
		}
		return apacheHeaderShell
	}
	return ""
}

func splice(body []byte, header string, kind sourceKind) []byte {
	switch kind {
	case kindGo:
		return spliceGo(body, header)
	case kindProto:
		return spliceProto(body, header)
	case kindTS:
		return spliceTS(body, header)
	case kindSQL:
		return spliceSQL(body, header)
	case kindShell:
		return spliceShell(body, header)
	}
	return concat([]byte(header), []byte("\n"), body)
}

func spliceGo(body []byte, header string) []byte {
	first := firstLine(body)
	if strings.HasPrefix(first, "// Code generated") {
		idx := bytes.IndexByte(body, '\n')
		return concat(body[:idx+1], []byte(header), []byte("\n"), body[idx+1:])
	}
	return concat([]byte(header), []byte("\n"), body)
}

func spliceTS(body []byte, header string) []byte {
	first := firstLine(body)
	if strings.HasPrefix(first, "#!") {
		idx := bytes.IndexByte(body, '\n')
		return concat(body[:idx+1], []byte(header), []byte("\n"), body[idx+1:])
	}
	return concat([]byte(header), []byte("\n"), body)
}

func spliceProto(body []byte, header string) []byte {
	return concat([]byte(header), []byte("\n"), body)
}

func spliceSQL(body []byte, header string) []byte {
	return concat([]byte(header), []byte("\n"), body)
}

func spliceShell(body []byte, header string) []byte {
	first := firstLine(body)
	if strings.HasPrefix(first, "#!") {
		idx := bytes.IndexByte(body, '\n')
		return concat(body[:idx+1], []byte(header), []byte("\n"), body[idx+1:])
	}
	return concat([]byte(header), []byte("\n"), body)
}

func firstLine(body []byte) string {
	if i := bytes.IndexByte(body, '\n'); i >= 0 {
		return string(body[:i])
	}
	return string(body)
}

func concat(parts ...[]byte) []byte {
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	out := make([]byte, 0, total)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
