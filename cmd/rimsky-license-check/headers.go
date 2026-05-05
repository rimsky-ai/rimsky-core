// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// headers.go — header text constants, header detection, and the
// stamp/verify implementations.

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"
)

// Header text written by --stamp. The marker substrings are what verify
// looks for in the first 10 lines of an existing file.

const (
	markerLicensedUnder = "Licensed under"           // any "Licensed under …" line counts as a header
	markerApache        = "Apache License"           // distinguishes apache headers
	markerDualAGPL      = "Dual-licensed under AGPL" // distinguishes agpl headers
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

const apacheHeaderProto = `// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
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

// violation records one header- or import-rule failure for end-of-run reporting.
type violation struct {
	path    string
	message string
}

// verifyHeaders checks each file's first ~10 lines for the right marker.
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

// detectHeader reads up to 10 lines and reports whether a license header
// is present and which kind.
func detectHeader(path string) (hasHeader, isApache, isAGPL bool, err error) {
	fh, err := os.Open(path)
	if err != nil {
		return false, false, false, err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	for i := 0; i < 10 && sc.Scan(); i++ {
		line := sc.Text()
		if strings.Contains(line, markerLicensedUnder) {
			hasHeader = true
		}
		if strings.Contains(line, markerApache) {
			isApache = true
		}
		if strings.Contains(line, markerDualAGPL) {
			isAGPL = true
			hasHeader = true
		}
	}
	if err := sc.Err(); err != nil {
		return false, false, false, err
	}
	return hasHeader, isApache, isAGPL, nil
}

// stampHeaders writes the appropriate header to any file that lacks one.
// Idempotent: files that already have a header are left untouched.
func stampHeaders(files []fileEntry) (int, error) {
	stamped := 0
	for _, f := range files {
		if f.classification == classUnknown {
			return stamped, fmt.Errorf("%s: unclassified source file", f.relPath)
		}
		hasHeader, _, _, err := detectHeader(f.absPath)
		if err != nil {
			return stamped, fmt.Errorf("%s: %w", f.relPath, err)
		}
		if hasHeader {
			continue
		}
		if err := stampOne(f); err != nil {
			return stamped, fmt.Errorf("%s: %w", f.relPath, err)
		}
		stamped++
	}
	return stamped, nil
}

func stampOne(f fileEntry) error {
	body, err := os.ReadFile(f.absPath)
	if err != nil {
		return err
	}
	header := headerFor(f)
	newContents := splice(body, header, f.kind)
	return os.WriteFile(f.absPath, newContents, 0o644)
}

func headerFor(f fileEntry) string {
	switch f.kind {
	case kindGo:
		if f.classification == classAGPL {
			return agplHeaderGo
		}
		return apacheHeaderGo
	case kindTS:
		// All TS in this repo is Apache per the boundary map.
		return apacheHeaderTS
	case kindProto:
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

// splice inserts the header in the right place per language convention:
//   - Go: before the package declaration; preserve a leading
//     "// Code generated …" line if present.
//   - Proto: at the very top (proto3 accepts leading comments before
//     `syntax`).
//   - TS: at the top, but preserve a leading shebang line if present.
//   - SQL: at the very top (no shebang convention).
//   - Shell: at the top, but preserve a leading shebang line if present.
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
		// Preserve the generated marker on top, then header, then rest.
		idx := bytes.IndexByte(body, '\n')
		return concat(body[:idx+1], []byte(header), []byte("\n"), body[idx+1:])
	}
	// Default: header then a blank line then the original body.
	return concat([]byte(header), []byte("\n"), body)
}

func spliceTS(body []byte, header string) []byte {
	first := firstLine(body)
	if strings.HasPrefix(first, "#!") {
		// Preserve the shebang on top, then header, then rest.
		idx := bytes.IndexByte(body, '\n')
		return concat(body[:idx+1], []byte(header), []byte("\n"), body[idx+1:])
	}
	return concat([]byte(header), []byte("\n"), body)
}

func spliceProto(body []byte, header string) []byte {
	// Proto3 accepts leading comments before `syntax`; put the header at
	// the very top so the verify-scan window finds it without needing to
	// skip multi-line documentation comments.
	return concat([]byte(header), []byte("\n"), body)
}

func spliceSQL(body []byte, header string) []byte {
	// SQL has no shebang convention; header goes at the very top.
	return concat([]byte(header), []byte("\n"), body)
}

func spliceShell(body []byte, header string) []byte {
	first := firstLine(body)
	if strings.HasPrefix(first, "#!") {
		// Preserve the shebang on top, then header, then rest.
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
