# Public documentation architecture — Implementation Plan

**Goal:** Build the rimsky public-documentation surface — concept files, protocol guides, agent-path indices, thin human path, generators, and CI lints — as defined in `docs/specs/2026-05-04-public-docs-architecture-design.md`.

**Architecture:** The public surface lives at `docs/{concepts,protocols,agents,humans}/` plus generated `docs/glossary.md`, hand-curated `docs/vocabulary.md`, and repo-root `llms.txt` / `llms-full.txt` copies. Three new Go binaries under `cmd/` produce the generated artifacts and run six lint checks. The public surface is fully self-contained: it cites within itself and into `protocols/proto/v1/*.proto` (the public wire contract); never into `docs/internal/`, `docs/specs/`, `docs/plans/`, `docs/history/`, `docs/future-work/`, `docs/examples/`, or non-proto source code.

**Tech Stack:** Go (root module `github.com/fallguy/rimsky`) for tooling — `gopkg.in/yaml.v3` for frontmatter parsing, stdlib only for everything else; markdown for content; existing Makefile for orchestration.

---

## Pre-reading (before starting)

The spec is the source of truth for *what to build*. This plan is the source of truth for *how to build it*. Read both:

1. **`docs/specs/2026-05-04-public-docs-architecture-design.md`** — full spec. Refer back for any decision rationale not repeated in this plan.
2. **`docs/internal/glossary.md`** — primary lift source for concept-file definitions. The four-layer model and per-term entries here seed `docs/concepts/`.
3. **`docs/specs/2026-05-04-foundation-contract.md`**, **`docs/specs/2026-05-04-modeling-layer-contract.md`**, **`docs/specs/2026-05-04-service-protocol-contract.md`** — secondary lift sources for "Why it exists" and "Consumer-visible guarantees" content per concept.
4. **`docs/internal/node-graph-design.md`** — long-form conceptual reference; lift narrative material for "Why it exists" sections.
5. **`docs/internal/claim-producer-author-guide.md`**, **`docs/internal/executor-author-guide.md`** — lift sources for `docs/protocols/claim-producer.md` and `docs/protocols/executor.md`.
6. **`docs/specs/2026-05-02-dashboard-and-observability-design.md`** — lift source for `docs/humans/dashboard.md`.
7. **`deploy/rimsky.yml`** — lift source for `docs/agents/examples/minimal-rimsky-yml.md`.
8. **`protocols/proto/v1/*.proto`** — the public wire contract; concept-file `proto_symbol` anchors must reference real symbols here.
9. **`CLAUDE.md`** — the blessed-invariants list and gotchas section provide consumer-visible guarantees worth surfacing on relevant concept files. Do not reference invariant numbers in public docs (they're internal); rephrase the property in plain prose.
10. **Existing `cmd/` binaries** — pattern-match on `cmd/rimsky-cli/main.go` for the file header (Apache 2.0), import shape, and main() structure. New binaries belong in the root module (`github.com/fallguy/rimsky`), so their imports look the same.

**Lift discipline:** when a fact is lifted from any of the above sources into the public surface, restructure it for the per-concept-file shape (§3 of the spec). After lift, the public-surface file is authoritative; the internal source is *not* updated to match. Do not edit `docs/internal/*` or `docs/specs/*` as part of this plan beyond the one concession in Task 17 (adding the unmaintained notice to `docs/internal/README.md`).

**Self-containment discipline:** The public surface MUST NOT contain links, references, or `<!-- @source: ... -->` citations to anything in `docs/internal/`, `docs/specs/`, `docs/plans/`, `docs/history/`, `docs/future-work/`, `docs/examples/`. Proto file paths under `protocols/proto/v1/` are the only citable source artifacts. The citation-drift lint (Task 11) enforces target paths under `docs/concepts/`; the vocabulary lint (Task 10) and frontmatter lint (Task 8) enforce structural cleanliness.

---

## File map

**Tooling — three new Go binaries under root module (`github.com/fallguy/rimsky`):**
- `cmd/rimsky-docs-glossary/main.go` — generates `docs/glossary.md` from `docs/concepts/*.md` frontmatter.
- `cmd/rimsky-docs-llms-full/main.go` — generates `docs/agents/llms-full.txt` from `docs/concepts/*.md` and `docs/protocols/*.md` bodies.
- `cmd/rimsky-docs-lint/main.go` — runs six lint subcommands.

**Public surface:**
- `docs/README.md` — doc-tree map.
- `docs/glossary.md` — generated; do not hand-edit.
- `docs/vocabulary.md` — hand-curated.
- `docs/concepts/README.md` + 23 concept files (see Task 18 for the canonical list).
- `docs/protocols/README.md` + 3 protocol-implementation guides.
- `docs/agents/llms.txt` — curated llmstxt.org index.
- `docs/agents/llms-full.txt` — generated.
- `docs/agents/errors/README.md` + per-error files.
- `docs/agents/examples/README.md` + 6 worked examples.
- `docs/humans/landing.md`, `docs/humans/concepts.md`, `docs/humans/dashboard.md`.

**Configuration:**
- `docs/.vocabulary-lint.yml` — forbidden-terms config.

**Repo root:**
- `llms.txt` — byte-equal copy of `docs/agents/llms.txt`.
- `llms-full.txt` — byte-equal copy of `docs/agents/llms-full.txt`.

**Build wiring:**
- `Makefile` — new targets: `docs-glossary`, `docs-llms-full`, `docs-lint`, `docs-roots`, `docs-build`.
- `licensing.yml` — add `cmd/rimsky-docs-glossary`, `cmd/rimsky-docs-llms-full`, `cmd/rimsky-docs-lint` as Apache-classified entries.

**Touched but not rewritten:**
- `CLAUDE.md` — light update to "Where to look first" section.
- `docs/internal/README.md` — added with unmaintained-notice content.
- `.github/workflows/*.yml` (or equivalent CI config) — add `make docs-lint` step.

---

## Task 1 — Create `cmd/rimsky-docs-glossary/` skeleton

**Files:** `cmd/rimsky-docs-glossary/main.go`, `cmd/rimsky-docs-glossary/main_test.go`, `cmd/rimsky-docs-glossary/testdata/concepts/<fixture>.md`.

**Steps:**

1. Create `cmd/rimsky-docs-glossary/main.go` with the following skeleton. Use the Apache 2.0 header pattern from `cmd/rimsky-cli/main.go` (first three comment lines) and adapt the description.

   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
   // repo root, or http://www.apache.org/licenses/LICENSE-2.0.

   // main.go — rimsky-docs-glossary. Reads docs/concepts/*.md frontmatter
   // and emits docs/glossary.md.
   package main

   import (
       "flag"
       "fmt"
       "os"
   )

   func main() {
       conceptsDir := flag.String("concepts-dir", "docs/concepts", "path to concept files")
       outputFile := flag.String("output", "docs/glossary.md", "path to write generated glossary")
       check := flag.Bool("check", false, "verify existing output matches generated; exit non-zero on diff")
       flag.Parse()

       if err := run(*conceptsDir, *outputFile, *check); err != nil {
           fmt.Fprintln(os.Stderr, err)
           os.Exit(1)
       }
   }

   func run(conceptsDir, outputPath string, check bool) error {
       // implemented in Task 2
       return fmt.Errorf("not yet implemented")
   }
   ```

2. Create one fixture concept file at `cmd/rimsky-docs-glossary/testdata/concepts/example.md`:

   ```markdown
   ---
   concept: example
   definition: |
     A fixture concept used by the glossary generator's tests.
   proto_symbol: (none)
   config_field: (none)
   api_surface: (none)
   related: []
   deprecated_terms: []
   ---

   # Example

   ## Definition

   A fixture concept used by the glossary generator's tests.
   ```

3. Create `cmd/rimsky-docs-glossary/main_test.go` with a placeholder test:

   ```go
   package main

   import "testing"

   func TestRunPlaceholder(t *testing.T) {
       t.Skip("implemented in task 2")
   }
   ```

**Verification:**

```sh
go build ./cmd/rimsky-docs-glossary/
go test ./cmd/rimsky-docs-glossary/
```

Both must exit 0.

---

## Task 2 — Implement glossary generator

**Files:** `cmd/rimsky-docs-glossary/main.go`, `cmd/rimsky-docs-glossary/parse.go`, `cmd/rimsky-docs-glossary/generate.go`, `cmd/rimsky-docs-glossary/main_test.go`, additional fixture files.

**Steps:**

1. Add `gopkg.in/yaml.v3` to the root module:

   ```sh
   go get gopkg.in/yaml.v3
   ```

2. Create `cmd/rimsky-docs-glossary/parse.go` with the frontmatter parser.

   ```go
   package main

   import (
       "bytes"
       "fmt"
       "os"

       "gopkg.in/yaml.v3"
   )

   type LayerSense struct {
       Layer string `yaml:"layer"`
       Sense string `yaml:"sense"`
   }

   type Frontmatter struct {
       Concept         string       `yaml:"concept"`
       Definition      string       `yaml:"definition"`
       ProtoSymbol    string       `yaml:"proto_symbol"`
       ConfigField     string       `yaml:"config_field"`
       APISurface      string       `yaml:"api_surface"`
       Related         []string     `yaml:"related"`
       DeprecatedTerms []string     `yaml:"deprecated_terms"`
       LayerSenses     []LayerSense `yaml:"layer_senses,omitempty"`
   }

   // ParseFrontmatter reads a markdown file and extracts its YAML frontmatter.
   // Returns an error if the file does not start with a `---` line or the
   // frontmatter does not parse.
   func ParseFrontmatter(path string) (*Frontmatter, error) {
       raw, err := os.ReadFile(path)
       if err != nil {
           return nil, fmt.Errorf("%s: %w", path, err)
       }
       if !bytes.HasPrefix(raw, []byte("---\n")) {
           return nil, fmt.Errorf("%s: missing frontmatter (file must start with `---`)", path)
       }
       end := bytes.Index(raw[4:], []byte("\n---\n"))
       if end < 0 {
           return nil, fmt.Errorf("%s: unterminated frontmatter", path)
       }
       fm := &Frontmatter{}
       if err := yaml.Unmarshal(raw[4:4+end], fm); err != nil {
           return nil, fmt.Errorf("%s: %w", path, err)
       }
       return fm, nil
   }
   ```

3. Create `cmd/rimsky-docs-glossary/generate.go` with the generator.

   ```go
   package main

   import (
       "bytes"
       "fmt"
       "os"
       "path/filepath"
       "sort"
       "strings"
   )

   const autogenWarning = "<!-- AUTOGENERATED from docs/concepts/. Do not edit by hand. Run `make docs-glossary`. -->\n"

   func generate(conceptsDir string) ([]byte, error) {
       entries, err := os.ReadDir(conceptsDir)
       if err != nil {
           return nil, err
       }
       var fms []*Frontmatter
       deprecatedTermsByCurrent := map[string][]string{}
       for _, e := range entries {
           if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "README.md" {
               continue
           }
           fm, err := ParseFrontmatter(filepath.Join(conceptsDir, e.Name()))
           if err != nil {
               return nil, err
           }
           fms = append(fms, fm)
           for _, term := range fm.DeprecatedTerms {
               deprecatedTermsByCurrent[fm.Concept] = append(deprecatedTermsByCurrent[fm.Concept], term)
           }
       }
       sort.Slice(fms, func(i, j int) bool { return fms[i].Concept < fms[j].Concept })

       var b bytes.Buffer
       b.WriteString(autogenWarning)
       b.WriteString("\n# Rimsky public glossary\n\n")
       b.WriteString("Authoritative public-surface vocabulary, generated from `docs/concepts/`. ")
       b.WriteString("For deeper material on each entry, follow the link to the concept file.\n\n")

       b.WriteString("## Concepts\n\n")
       b.WriteString("| Concept | Definition |\n|---|---|\n")
       for _, fm := range fms {
           def := strings.TrimSpace(strings.ReplaceAll(fm.Definition, "\n", " "))
           // collapse internal whitespace
           def = strings.Join(strings.Fields(def), " ")
           link := fmt.Sprintf("[%s](concepts/%s.md)", fm.Concept, fm.Concept)
           b.WriteString(fmt.Sprintf("| %s | %s |\n", link, def))
       }
       b.WriteString("\n")

       // Layered senses inline section
       hasLayered := false
       for _, fm := range fms {
           if len(fm.LayerSenses) > 0 {
               hasLayered = true
               break
           }
       }
       if hasLayered {
           b.WriteString("## Layered senses\n\n")
           b.WriteString("Some terms have layered presentations across the four-layer model. ")
           b.WriteString("See [`concepts/four-layer-model.md`](concepts/four-layer-model.md).\n\n")
           for _, fm := range fms {
               if len(fm.LayerSenses) == 0 {
                   continue
               }
               b.WriteString(fmt.Sprintf("### %s\n\n", fm.Concept))
               for _, ls := range fm.LayerSenses {
                   b.WriteString(fmt.Sprintf("- **%s layer**: %s\n", ls.Layer, ls.Sense))
               }
               b.WriteString("\n")
           }
       }

       // Deprecated terms section
       hasDeprecated := false
       for _, terms := range deprecatedTermsByCurrent {
           if len(terms) > 0 {
               hasDeprecated = true
               break
           }
       }
       if hasDeprecated {
           b.WriteString("## Deprecated terms\n\n")
           b.WriteString("| Deprecated | Current |\n|---|---|\n")
           type entry struct{ deprecated, current string }
           var rows []entry
           for current, terms := range deprecatedTermsByCurrent {
               for _, term := range terms {
                   rows = append(rows, entry{deprecated: term, current: current})
               }
           }
           sort.Slice(rows, func(i, j int) bool { return rows[i].deprecated < rows[j].deprecated })
           for _, r := range rows {
               b.WriteString(fmt.Sprintf("| `%s` | [%s](concepts/%s.md) |\n", r.deprecated, r.current, r.current))
           }
           b.WriteString("\n")
       }

       return b.Bytes(), nil
   }
   ```

4. Update `main.go::run` to call `generate` and either write the output or compare against the existing file:

   ```go
   func run(conceptsDir, outputPath string, check bool) error {
       got, err := generate(conceptsDir)
       if err != nil {
           return err
       }
       if check {
           want, err := os.ReadFile(outputPath)
           if err != nil {
               return fmt.Errorf("%s: %w", outputPath, err)
           }
           if !bytes.Equal(got, want) {
               return fmt.Errorf("%s differs from generator output; run `make docs-glossary` to regenerate", outputPath)
           }
           return nil
       }
       return os.WriteFile(outputPath, got, 0644)
   }
   ```

   Add `"bytes"` to the imports.

5. Add fixture files for tests.

   `cmd/rimsky-docs-glossary/testdata/concepts/two.md`:

   ```markdown
   ---
   concept: two
   definition: |
     The second fixture concept, used to test sorting.
   proto_symbol: (none)
   config_field: (none)
   api_surface: (none)
   related: [example]
   deprecated_terms: [legacy_two]
   ---

   # Two
   ```

6. Replace `main_test.go` with concrete tests:

   ```go
   package main

   import (
       "bytes"
       "os"
       "path/filepath"
       "strings"
       "testing"
   )

   func TestGenerate_FixtureProducesSortedOutput(t *testing.T) {
       got, err := generate("testdata/concepts")
       if err != nil {
           t.Fatalf("generate: %v", err)
       }
       s := string(got)
       if !strings.HasPrefix(s, "<!-- AUTOGENERATED") {
           t.Errorf("missing autogen warning, got prefix %q", s[:60])
       }
       // example must appear before two (alphabetical)
       iEx := strings.Index(s, "[example]")
       iTwo := strings.Index(s, "[two]")
       if iEx < 0 || iTwo < 0 || iEx > iTwo {
           t.Errorf("expected example before two; iEx=%d iTwo=%d", iEx, iTwo)
       }
       // deprecated table includes legacy_two
       if !strings.Contains(s, "`legacy_two`") {
           t.Error("expected deprecated terms table to include legacy_two")
       }
   }

   func TestRun_CheckMode_DetectsDrift(t *testing.T) {
       tmp := t.TempDir()
       outPath := filepath.Join(tmp, "glossary.md")
       if err := os.WriteFile(outPath, []byte("stale content"), 0644); err != nil {
           t.Fatal(err)
       }
       err := run("testdata/concepts", outPath, true)
       if err == nil {
           t.Fatal("expected drift error, got nil")
       }
       if !strings.Contains(err.Error(), "differs from generator output") {
           t.Errorf("unexpected error: %v", err)
       }
   }

   func TestRun_WriteMode_ProducesByteEqualOutput(t *testing.T) {
       tmp := t.TempDir()
       outPath := filepath.Join(tmp, "glossary.md")
       if err := run("testdata/concepts", outPath, false); err != nil {
           t.Fatalf("run write: %v", err)
       }
       wrote, err := os.ReadFile(outPath)
       if err != nil {
           t.Fatal(err)
       }
       generated, err := generate("testdata/concepts")
       if err != nil {
           t.Fatal(err)
       }
       if !bytes.Equal(wrote, generated) {
           t.Error("write-mode output does not byte-equal generator output")
       }
   }
   ```

7. Add the Makefile target. Append to `Makefile` after the `tidy:` block:

   ```makefile
   docs-glossary:
   	go run ./cmd/rimsky-docs-glossary
   ```

   Add `docs-glossary` to the `.PHONY` line at the top of the Makefile.

8. Add `cmd/rimsky-docs-glossary` to `licensing.yml` under whatever the existing pattern is for cmd entries (look at how `cmd/rimsky-license-check` is classified there; copy that pattern for the three new docs binaries).

**Verification:**

```sh
go build ./cmd/rimsky-docs-glossary/
go test ./cmd/rimsky-docs-glossary/
go run ./cmd/rimsky-license-check
```

All three exit 0. (The third confirms the licensing.yml addition is well-formed.)

---

## Task 3 — Create `cmd/rimsky-docs-llms-full/` and implement generator

**Files:** `cmd/rimsky-docs-llms-full/main.go`, `cmd/rimsky-docs-llms-full/main_test.go`, `cmd/rimsky-docs-llms-full/testdata/`.

**Steps:**

1. Create `cmd/rimsky-docs-llms-full/main.go`:

   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
   // repo root, or http://www.apache.org/licenses/LICENSE-2.0.

   // main.go — rimsky-docs-llms-full. Concatenates docs/concepts/*.md and
   // docs/protocols/*.md (frontmatter stripped, body included) into
   // docs/agents/llms-full.txt for single-pull retrieval.
   package main

   import (
       "bytes"
       "flag"
       "fmt"
       "os"
       "path/filepath"
       "sort"
       "strings"
   )

   func main() {
       conceptsDir := flag.String("concepts-dir", "docs/concepts", "path to concept files")
       protocolsDir := flag.String("protocols-dir", "docs/protocols", "path to protocol guides")
       outputFile := flag.String("output", "docs/agents/llms-full.txt", "path to write generated llms-full.txt")
       check := flag.Bool("check", false, "verify existing output matches generated; exit non-zero on diff")
       flag.Parse()
       if err := run(*conceptsDir, *protocolsDir, *outputFile, *check); err != nil {
           fmt.Fprintln(os.Stderr, err)
           os.Exit(1)
       }
   }

   func run(conceptsDir, protocolsDir, outputPath string, check bool) error {
       got, err := generate(conceptsDir, protocolsDir)
       if err != nil {
           return err
       }
       if check {
           want, err := os.ReadFile(outputPath)
           if err != nil {
               return fmt.Errorf("%s: %w", outputPath, err)
           }
           if !bytes.Equal(got, want) {
               return fmt.Errorf("%s differs from generator output; run `make docs-llms-full` to regenerate", outputPath)
           }
           return nil
       }
       return os.WriteFile(outputPath, got, 0644)
   }

   func generate(conceptsDir, protocolsDir string) ([]byte, error) {
       var b bytes.Buffer
       b.WriteString("# Rimsky — full canonical content\n\n")
       b.WriteString("Generated by rimsky-docs-llms-full. For agents whose tooling can fetch ")
       b.WriteString("a single file but not crawl. Source of truth is `docs/concepts/` and `docs/protocols/`.\n\n")

       b.WriteString("---\n\n# Concepts\n\n")
       if err := concatBodies(&b, conceptsDir); err != nil {
           return nil, err
       }
       b.WriteString("---\n\n# Protocols\n\n")
       if err := concatBodies(&b, protocolsDir); err != nil {
           return nil, err
       }
       return b.Bytes(), nil
   }

   func concatBodies(b *bytes.Buffer, dir string) error {
       entries, err := os.ReadDir(dir)
       if err != nil {
           return err
       }
       var names []string
       for _, e := range entries {
           if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "README.md" {
               continue
           }
           names = append(names, e.Name())
       }
       sort.Strings(names)
       for _, name := range names {
           raw, err := os.ReadFile(filepath.Join(dir, name))
           if err != nil {
               return err
           }
           body := stripFrontmatter(raw)
           b.Write(body)
           b.WriteString("\n\n---\n\n")
       }
       return nil
   }

   func stripFrontmatter(raw []byte) []byte {
       if !bytes.HasPrefix(raw, []byte("---\n")) {
           return raw
       }
       end := bytes.Index(raw[4:], []byte("\n---\n"))
       if end < 0 {
           return raw
       }
       return bytes.TrimLeft(raw[4+end+5:], "\n")
   }
   ```

2. Create test fixtures and `main_test.go`:

   `cmd/rimsky-docs-llms-full/testdata/concepts/alpha.md`:
   ```markdown
   ---
   concept: alpha
   definition: |
     Alpha fixture.
   proto_symbol: (none)
   config_field: (none)
   api_surface: (none)
   related: []
   deprecated_terms: []
   ---

   # Alpha

   Body for alpha.
   ```

   `cmd/rimsky-docs-llms-full/testdata/concepts/beta.md`:
   ```markdown
   ---
   concept: beta
   definition: |
     Beta fixture.
   proto_symbol: (none)
   config_field: (none)
   api_surface: (none)
   related: []
   deprecated_terms: []
   ---

   # Beta

   Body for beta.
   ```

   `cmd/rimsky-docs-llms-full/testdata/protocols/gamma.md`:
   ```markdown
   # Gamma protocol

   No frontmatter on protocol guides; body included verbatim.
   ```

   `cmd/rimsky-docs-llms-full/main_test.go`:
   ```go
   package main

   import (
       "strings"
       "testing"
   )

   func TestGenerate_OrdersAlphabeticallyAndStripsFrontmatter(t *testing.T) {
       got, err := generate("testdata/concepts", "testdata/protocols")
       if err != nil {
           t.Fatalf("generate: %v", err)
       }
       s := string(got)
       if strings.Contains(s, "---\nconcept: alpha") {
           t.Error("frontmatter not stripped from alpha")
       }
       if !strings.Contains(s, "Body for alpha.") || !strings.Contains(s, "Body for beta.") {
           t.Error("expected both body texts in output")
       }
       if !strings.Contains(s, "Gamma protocol") {
           t.Error("expected protocols body")
       }
       iAlpha := strings.Index(s, "Body for alpha.")
       iBeta := strings.Index(s, "Body for beta.")
       if iAlpha < 0 || iBeta < 0 || iAlpha > iBeta {
           t.Errorf("alpha must precede beta; iAlpha=%d iBeta=%d", iAlpha, iBeta)
       }
   }
   ```

3. Append to Makefile:

   ```makefile
   docs-llms-full:
   	go run ./cmd/rimsky-docs-llms-full
   ```

   Add `docs-llms-full` to `.PHONY`.

4. Add `cmd/rimsky-docs-llms-full` to `licensing.yml` (Apache).

**Verification:**

```sh
go build ./cmd/rimsky-docs-llms-full/
go test ./cmd/rimsky-docs-llms-full/
go run ./cmd/rimsky-license-check
```

---

## Task 4 — Create `cmd/rimsky-docs-lint/` skeleton with subcommand routing

**Files:** `cmd/rimsky-docs-lint/main.go`, `cmd/rimsky-docs-lint/main_test.go`.

**Steps:**

1. Create `cmd/rimsky-docs-lint/main.go`:

   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
   // repo root, or http://www.apache.org/licenses/LICENSE-2.0.

   // main.go — rimsky-docs-lint. Six subcommands enforce structural integrity
   // of the public-documentation surface (docs/concepts/, docs/protocols/,
   // docs/agents/, docs/humans/, docs/glossary.md, docs/vocabulary.md).
   package main

   import (
       "fmt"
       "os"
   )

   type subcommand struct {
       name string
       fn   func(args []string) error
       desc string
   }

   var subcommands = []subcommand{
       {"frontmatter", runFrontmatter, "validate frontmatter shape on all concept files"},
       {"glossary-parity", runGlossaryParity, "verify docs/glossary.md matches generator output"},
       {"vocabulary", runVocabulary, "scan public surface for forbidden terms"},
       {"citation-drift", runCitationDrift, "verify @source: citations match canonical definitions"},
       {"public-anchor-validity", runPublicAnchorValidity, "verify proto_symbol / config_field / api_surface anchors"},
       {"llms-txt-validity", runLLMSTxtValidity, "verify llms.txt is well-formed and links resolve"},
       {"all", runAll, "run all six lints; exits non-zero if any fail"},
   }

   func main() {
       if len(os.Args) < 2 {
           usage()
           os.Exit(2)
       }
       cmd := os.Args[1]
       for _, sc := range subcommands {
           if sc.name == cmd {
               if err := sc.fn(os.Args[2:]); err != nil {
                   fmt.Fprintln(os.Stderr, err)
                   os.Exit(1)
               }
               return
           }
       }
       fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", cmd)
       usage()
       os.Exit(2)
   }

   func usage() {
       fmt.Fprintln(os.Stderr, "usage: rimsky-docs-lint <subcommand> [flags]")
       fmt.Fprintln(os.Stderr, "subcommands:")
       for _, sc := range subcommands {
           fmt.Fprintf(os.Stderr, "  %-25s %s\n", sc.name, sc.desc)
       }
   }

   // Each subcommand is implemented in its own file (Tasks 5-10).
   // This file only carries routing; the subcommand functions live in
   // frontmatter.go, glossary_parity.go, vocabulary.go, citation_drift.go,
   // public_anchor_validity.go, llms_txt_validity.go.

   func runAll(args []string) error {
       var errs []error
       for _, sc := range subcommands {
           if sc.name == "all" {
               continue
           }
           if err := sc.fn(args); err != nil {
               errs = append(errs, fmt.Errorf("%s: %w", sc.name, err))
               fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", sc.name, err)
           } else {
               fmt.Fprintf(os.Stderr, "OK   %s\n", sc.name)
           }
       }
       if len(errs) > 0 {
           return fmt.Errorf("%d lint(s) failed", len(errs))
       }
       return nil
   }
   ```

2. Create stub implementation files; each Task 5-10 fills one in. For now, each subcommand returns a "not yet implemented" error so the skeleton builds and routing is testable:

   `cmd/rimsky-docs-lint/frontmatter.go`:
   ```go
   package main

   import "errors"

   func runFrontmatter(args []string) error {
       return errors.New("frontmatter: not yet implemented")
   }
   ```

   Repeat the same one-line stub for `glossary_parity.go::runGlossaryParity`, `vocabulary.go::runVocabulary`, `citation_drift.go::runCitationDrift`, `public_anchor_validity.go::runPublicAnchorValidity`, `llms_txt_validity.go::runLLMSTxtValidity`.

3. Add `cmd/rimsky-docs-lint` to `licensing.yml` (Apache).

4. Append to Makefile:

   ```makefile
   docs-lint:
   	go run ./cmd/rimsky-docs-lint all
   ```

   Add `docs-lint` to `.PHONY`.

**Verification:**

```sh
go build ./cmd/rimsky-docs-lint/
# unknown-subcommand error path
go run ./cmd/rimsky-docs-lint nonexistent 2>&1 | grep -q "unknown subcommand: nonexistent" && echo OK
# usage
go run ./cmd/rimsky-docs-lint 2>&1 | grep -q "usage: rimsky-docs-lint" && echo OK
go run ./cmd/rimsky-license-check
```

All exits 0.

---

## Task 5 — Implement `frontmatter` subcommand

**Files:** `cmd/rimsky-docs-lint/frontmatter.go`, `cmd/rimsky-docs-lint/frontmatter_test.go`, `cmd/rimsky-docs-lint/testdata/`.

**Steps:**

1. Replace `frontmatter.go`:

   ```go
   package main

   import (
       "bytes"
       "flag"
       "fmt"
       "os"
       "path/filepath"
       "strings"

       "gopkg.in/yaml.v3"
   )

   type fmShape struct {
       Concept         string                   `yaml:"concept"`
       Definition      string                   `yaml:"definition"`
       ProtoSymbol    string                   `yaml:"proto_symbol"`
       ConfigField     string                   `yaml:"config_field"`
       APISurface      string                   `yaml:"api_surface"`
       Related         []string                 `yaml:"related"`
       DeprecatedTerms []string                 `yaml:"deprecated_terms"`
       LayerSenses     []map[string]interface{} `yaml:"layer_senses,omitempty"`
   }

   func runFrontmatter(args []string) error {
       fs := flag.NewFlagSet("frontmatter", flag.ContinueOnError)
       dir := fs.String("dir", "docs/concepts", "concept directory to validate")
       if err := fs.Parse(args); err != nil {
           return err
       }
       entries, err := os.ReadDir(*dir)
       if err != nil {
           return err
       }
       var problems []string
       for _, e := range entries {
           if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "README.md" {
               continue
           }
           if err := validateFile(filepath.Join(*dir, e.Name())); err != nil {
               problems = append(problems, err.Error())
           }
       }
       if len(problems) > 0 {
           return fmt.Errorf("frontmatter validation failed:\n  - %s", strings.Join(problems, "\n  - "))
       }
       return nil
   }

   func validateFile(path string) error {
       raw, err := os.ReadFile(path)
       if err != nil {
           return err
       }
       if !bytes.HasPrefix(raw, []byte("---\n")) {
           return fmt.Errorf("%s: missing frontmatter (must start with `---`)", path)
       }
       end := bytes.Index(raw[4:], []byte("\n---\n"))
       if end < 0 {
           return fmt.Errorf("%s: unterminated frontmatter", path)
       }
       fm := &fmShape{}
       dec := yaml.NewDecoder(bytes.NewReader(raw[4 : 4+end]))
       dec.KnownFields(true)
       if err := dec.Decode(fm); err != nil {
           return fmt.Errorf("%s: %w", path, err)
       }
       var missing []string
       if strings.TrimSpace(fm.Concept) == "" {
           missing = append(missing, "concept")
       }
       if strings.TrimSpace(fm.Definition) == "" {
           missing = append(missing, "definition")
       }
       if strings.TrimSpace(fm.ProtoSymbol) == "" {
           missing = append(missing, "proto_symbol (use \"(none)\" if not applicable)")
       }
       if strings.TrimSpace(fm.ConfigField) == "" {
           missing = append(missing, "config_field (use \"(none)\" if not applicable)")
       }
       if strings.TrimSpace(fm.APISurface) == "" {
           missing = append(missing, "api_surface (use \"(none)\" if not applicable)")
       }
       if fm.Related == nil {
           missing = append(missing, "related (use [] if empty)")
       }
       if fm.DeprecatedTerms == nil {
           missing = append(missing, "deprecated_terms (use [] if empty)")
       }
       if len(missing) > 0 {
           return fmt.Errorf("%s: missing required field(s): %s", path, strings.Join(missing, ", "))
       }
       expectedConcept := strings.TrimSuffix(filepath.Base(path), ".md")
       if fm.Concept != expectedConcept {
           return fmt.Errorf("%s: frontmatter `concept: %s` does not match filename (expected %q)", path, fm.Concept, expectedConcept)
       }
       return nil
   }
   ```

2. Create test fixtures.

   `cmd/rimsky-docs-lint/testdata/frontmatter-good/example.md`:
   ```markdown
   ---
   concept: example
   definition: |
     A valid fixture.
   proto_symbol: (none)
   config_field: (none)
   api_surface: (none)
   related: []
   deprecated_terms: []
   ---

   # Example
   ```

   `cmd/rimsky-docs-lint/testdata/frontmatter-bad-missing-field/example.md`:
   ```markdown
   ---
   concept: example
   definition: |
     Missing proto_symbol and others.
   ---

   # Example
   ```

   `cmd/rimsky-docs-lint/testdata/frontmatter-bad-name-mismatch/wrong.md`:
   ```markdown
   ---
   concept: example
   definition: |
     Filename mismatch.
   proto_symbol: (none)
   config_field: (none)
   api_surface: (none)
   related: []
   deprecated_terms: []
   ---
   ```

3. Create `frontmatter_test.go`:

   ```go
   package main

   import (
       "strings"
       "testing"
   )

   func TestFrontmatter_GoodFixturePasses(t *testing.T) {
       if err := runFrontmatter([]string{"-dir=testdata/frontmatter-good"}); err != nil {
           t.Errorf("expected pass, got %v", err)
       }
   }

   func TestFrontmatter_MissingFieldFails(t *testing.T) {
       err := runFrontmatter([]string{"-dir=testdata/frontmatter-bad-missing-field"})
       if err == nil {
           t.Fatal("expected failure")
       }
       if !strings.Contains(err.Error(), "proto_symbol") {
           t.Errorf("expected proto_symbol in error, got %v", err)
       }
   }

   func TestFrontmatter_FilenameMismatchFails(t *testing.T) {
       err := runFrontmatter([]string{"-dir=testdata/frontmatter-bad-name-mismatch"})
       if err == nil {
           t.Fatal("expected failure")
       }
       if !strings.Contains(err.Error(), "does not match filename") {
           t.Errorf("expected filename-mismatch error, got %v", err)
       }
   }
   ```

**Verification:**

```sh
go test ./cmd/rimsky-docs-lint/
```

---

## Task 6 — Implement `glossary-parity` subcommand

**Files:** `cmd/rimsky-docs-lint/glossary_parity.go`, `cmd/rimsky-docs-lint/glossary_parity_test.go`.

**Steps:**

1. Replace `glossary_parity.go`. The subcommand shells out to `rimsky-docs-glossary -check=true`; it accepts a `-repo-root` flag that's used as the exec's working directory so the `go run ./cmd/rimsky-docs-glossary` resolves correctly regardless of caller cwd.

   ```go
   package main

   import (
       "flag"
       "fmt"
       "os"
       "os/exec"
   )

   func runGlossaryParity(args []string) error {
       fs := flag.NewFlagSet("glossary-parity", flag.ContinueOnError)
       outputPath := fs.String("output", "docs/glossary.md", "path to existing glossary file (relative to repo-root)")
       conceptsDir := fs.String("concepts-dir", "docs/concepts", "path to concept files (relative to repo-root)")
       repoRoot := fs.String("repo-root", ".", "repo root used as exec cwd so `go run ./cmd/rimsky-docs-glossary` resolves")
       if err := fs.Parse(args); err != nil {
           return err
       }
       cmd := exec.Command("go", "run", "./cmd/rimsky-docs-glossary",
           "-concepts-dir="+*conceptsDir, "-output="+*outputPath, "-check=true")
       cmd.Dir = *repoRoot
       cmd.Stderr = os.Stderr
       if err := cmd.Run(); err != nil {
           return fmt.Errorf("glossary parity failed: %w", err)
       }
       return nil
   }
   ```

2. Create `glossary_parity_test.go`. The test resolves the repo root by walking up from the package directory until it finds `go.work`. It then writes a stale glossary into a temp file and asserts that `runGlossaryParity` returns a drift error when invoked against the real glossary fixtures.

   ```go
   package main

   import (
       "os"
       "path/filepath"
       "strings"
       "testing"
   )

   // findRepoRoot walks up from the test's cwd looking for go.work. Tests run
   // with cwd = the package directory (cmd/rimsky-docs-lint/), so two levels up
   // is the repo root. We walk to be defensive against future relayouts.
   func findRepoRoot(t *testing.T) string {
       t.Helper()
       wd, err := os.Getwd()
       if err != nil {
           t.Fatal(err)
       }
       dir := wd
       for i := 0; i < 8; i++ {
           if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
               return dir
           }
           parent := filepath.Dir(dir)
           if parent == dir {
               break
           }
           dir = parent
       }
       t.Skipf("repo root not found above %s", wd)
       return ""
   }

   func TestGlossaryParity_DetectsDrift(t *testing.T) {
       repoRoot := findRepoRoot(t)
       tmp := t.TempDir()
       outAbs := filepath.Join(tmp, "glossary.md")
       if err := os.WriteFile(outAbs, []byte("stale content"), 0644); err != nil {
           t.Fatal(err)
       }
       // Both -concepts-dir and -output are passed to the inner binary as-is.
       // We want them to resolve from cmd.Dir = repoRoot, so we pass an absolute
       // path for the temp output and a repo-root-relative path for the fixtures.
       err := runGlossaryParity([]string{
           "-repo-root=" + repoRoot,
           "-concepts-dir=cmd/rimsky-docs-glossary/testdata/concepts",
           "-output=" + outAbs,
       })
       if err == nil {
           t.Fatal("expected drift error")
       }
       if !strings.Contains(err.Error(), "glossary parity failed") {
           t.Errorf("unexpected error shape: %v", err)
       }
   }

   func TestGlossaryParity_FixtureRoundTrip(t *testing.T) {
       repoRoot := findRepoRoot(t)
       tmp := t.TempDir()
       outAbs := filepath.Join(tmp, "glossary.md")
       // First, generate the canonical output by running with -check=false.
       genCmd := []string{
           "run", "./cmd/rimsky-docs-glossary",
           "-concepts-dir=cmd/rimsky-docs-glossary/testdata/concepts",
           "-output=" + outAbs,
       }
       if err := runGoCmd(t, repoRoot, genCmd); err != nil {
           t.Fatalf("generate: %v", err)
       }
       // Then, parity-check should pass.
       err := runGlossaryParity([]string{
           "-repo-root=" + repoRoot,
           "-concepts-dir=cmd/rimsky-docs-glossary/testdata/concepts",
           "-output=" + outAbs,
       })
       if err != nil {
           t.Errorf("expected parity pass after generate, got %v", err)
       }
   }
   ```

   Helper (also in the test file):

   ```go
   import "os/exec"

   func runGoCmd(t *testing.T, dir string, args []string) error {
       t.Helper()
       cmd := exec.Command("go", args...)
       cmd.Dir = dir
       cmd.Stderr = os.Stderr
       cmd.Stdout = os.Stderr
       return cmd.Run()
   }
   ```

   Move the `os/exec` import to the top of the file with the others if Go's import block already exists there.

**Verification:**

```sh
go test ./cmd/rimsky-docs-lint/
```

---

## Task 7 — Implement `vocabulary` subcommand and `docs/.vocabulary-lint.yml`

**Files:** `cmd/rimsky-docs-lint/vocabulary.go`, `cmd/rimsky-docs-lint/vocabulary_test.go`, `docs/.vocabulary-lint.yml`.

**Steps:**

1. Create `docs/.vocabulary-lint.yml` with the user-confirmed seed (`template_id`, `consumer_key`, `substrate`) plus the spec-§7.3-mandated additions (obsolete table names + protocol-layer `Store`) with concrete grep patterns. Each scope entry is a single-level glob (`*.md` or a literal file path); the lint does not implement `**` recursive matching — directories that contain markdown are listed individually, which keeps the matcher trivial and the scope auditable.

   ```yaml
   # Forbidden terms in the public-surface documentation.
   # Each entry has:
   #   term:        Go-flavored regex (RE2). The vocabulary lint compiles this as-is.
   #   replacement: free-text guidance shown in the lint failure message.
   #   scope:       list of single-level globs (filepath.Glob shapes). No `**`.
   #
   # The lint scope is the public surface only. Layered colloquialisms (e.g. the
   # bundled-services-layer "store") are disambiguated in concept-file prose, not
   # enforced via grep, so their patterns are NOT in this list.
   #
   # Per-line ignore convention: place `<!-- vocabulary-lint-ignore: <term> -->`
   # on the line *immediately preceding* the offending line. (A same-line trailing
   # comment also suppresses the hit on that line — see the lint's per-line logic.)

   forbidden:
     # User-confirmed seed (spec §7.3).
     - term: '\btemplate_id\b'
       replacement: template_hash
       scope: &public_surface_md
         - docs/concepts/*.md
         - docs/protocols/*.md
         - docs/agents/*.md
         - docs/agents/*.txt
         - docs/agents/errors/*.md
         - docs/agents/examples/*.md
         - docs/humans/*.md
         - docs/glossary.md
         - docs/vocabulary.md
         - docs/licensing.md
         - docs/README.md
         - llms.txt
         - llms-full.txt
         - README.md

     - term: '\bconsumer_key\b'
       replacement: instance_key
       scope: *public_surface_md

     - term: '\bsubstrate\b'
       replacement: 'store (bundled-services-layer) or claim producer (protocol-layer) per context'
       scope: *public_surface_md

     # Spec §7.3 additions (obsolete table names — strict word-boundary match).
     - term: '\brimsky_dispatch\b'
       replacement: rimsky_worker_request
       scope: *public_surface_md

     - term: '\brimsky_lock_holders\b'
       replacement: rimsky_claim_handle
       scope: *public_surface_md

     - term: '\brimsky_store_lifecycle\b'
       replacement: rimsky_lifecycle_idempotency
       scope: *public_surface_md

     # Protocol-layer interface name `Store`. The pattern matches only the
     # backticked form, which is the canonical way technical interface names are
     # cited in markdown. Bare-prose "Store" or compound colloquial forms
     # ("Filesystem Store", "Postgres Store") at the bundled-services layer are
     # NOT matched. If a concept file actually needs to write the literal
     # backticked `Store` (e.g. when calling out the deprecation), use the
     # per-line ignore convention.
     - term: '`Store`'
       replacement: '`ClaimProducer`'
       scope: *public_surface_md
   ```

2. Create `vocabulary.go`:

   ```go
   package main

   import (
       "bufio"
       "flag"
       "fmt"
       "os"
       "path/filepath"
       "regexp"
       "strings"

       "gopkg.in/yaml.v3"
   )

   type vocabConfig struct {
       Forbidden []vocabRule `yaml:"forbidden"`
   }

   type vocabRule struct {
       Term        string   `yaml:"term"`
       Replacement string   `yaml:"replacement"`
       Scope       []string `yaml:"scope"`
   }

   var ignoreCommentRE = regexp.MustCompile(`<!--\s*vocabulary-lint-ignore:\s*([^\s>]+)\s*-->`)

   func runVocabulary(args []string) error {
       fs := flag.NewFlagSet("vocabulary", flag.ContinueOnError)
       configPath := fs.String("config", "docs/.vocabulary-lint.yml", "path to vocabulary lint config")
       repoRoot := fs.String("repo-root", ".", "path to repository root for scope-glob resolution")
       if err := fs.Parse(args); err != nil {
           return err
       }
       cfg, err := loadVocabConfig(*configPath)
       if err != nil {
           return err
       }
       var hits []string
       for _, rule := range cfg.Forbidden {
           re, err := regexp.Compile(rule.Term)
           if err != nil {
               return fmt.Errorf("config: invalid regex %q: %w", rule.Term, err)
           }
           files, err := expandGlobs(*repoRoot, rule.Scope)
           if err != nil {
               return err
           }
           for _, path := range files {
               fileHits, err := scanFile(path, re, rule.Term, rule.Replacement)
               if err != nil {
                   return err
               }
               hits = append(hits, fileHits...)
           }
       }
       if len(hits) > 0 {
           return fmt.Errorf("vocabulary lint found %d issue(s):\n  - %s\n(see docs/vocabulary.md)",
               len(hits), strings.Join(hits, "\n  - "))
       }
       return nil
   }

   func loadVocabConfig(path string) (*vocabConfig, error) {
       raw, err := os.ReadFile(path)
       if err != nil {
           return nil, err
       }
       cfg := &vocabConfig{}
       if err := yaml.Unmarshal(raw, cfg); err != nil {
           return nil, err
       }
       return cfg, nil
   }

   // expandGlobs resolves each pattern via filepath.Glob (joined with repoRoot).
   // Single-level globs only — `**` is not supported. The .vocabulary-lint.yml
   // config enumerates concrete directories instead, which keeps the matcher
   // trivial and makes the lint's coverage auditable.
   func expandGlobs(root string, patterns []string) ([]string, error) {
       seen := map[string]struct{}{}
       var out []string
       for _, p := range patterns {
           if strings.Contains(p, "**") {
               return nil, fmt.Errorf("scope %q contains `**`; this lint expects single-level globs only", p)
           }
           full := filepath.Join(root, p)
           m, err := filepath.Glob(full)
           if err != nil {
               return nil, err
           }
           for _, match := range m {
               if _, ok := seen[match]; ok {
                   continue
               }
               seen[match] = struct{}{}
               out = append(out, match)
           }
       }
       return out, nil
   }

   func scanFile(path string, re *regexp.Regexp, term, replacement string) ([]string, error) {
       f, err := os.Open(path)
       if err != nil {
           if os.IsNotExist(err) {
               return nil, nil
           }
           return nil, err
       }
       defer f.Close()
       scanner := bufio.NewScanner(f)
       scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
       var hits []string
       lineno := 0
       skipNext := false
       for scanner.Scan() {
           lineno++
           line := scanner.Text()

           // If a previous line said "ignore the next line for term <X>", and
           // X matches this rule's term, skip the term-search on this line.
           if skipNext {
               skipNext = false
               continue
           }

           // Look for a vocabulary-lint-ignore comment on this line.
           if m := ignoreCommentRE.FindStringSubmatch(line); m != nil {
               ignoredTerm := m[1]
               // Decide whether the comment applies to this line (comment is
               // alongside the offending term) or to the next line (comment is
               // on its own line above the offender).
               stripped := ignoreCommentRE.ReplaceAllString(line, "")
               if termAppliesToLine(ignoredTerm, term) && re.FindStringIndex(stripped) != nil {
                   // Same-line ignore: do not flag this line; do not carry over.
                   continue
               }
               // Comment-only line preceding the offender: skip the next line.
               if termAppliesToLine(ignoredTerm, term) {
                   skipNext = true
               }
               continue
           }

           if loc := re.FindStringIndex(line); loc != nil {
               hits = append(hits, fmt.Sprintf("%s:%d  %q → %s",
                   path, lineno, line[loc[0]:loc[1]], replacement))
           }
       }
       return hits, scanner.Err()
   }

   // termAppliesToLine decides whether an ignore comment naming `ignoredTerm`
   // suppresses a hit for `ruleTerm`. Match-by-substring: if the ignore comment
   // names a term that appears in the rule's regex source, treat as applying.
   // Conservative — false positives just leave a line scanned, which is the
   // safe direction.
   func termAppliesToLine(ignoredTerm, ruleTerm string) bool {
       return strings.Contains(ruleTerm, ignoredTerm) || strings.Contains(ignoredTerm, ruleTerm)
   }
   ```

3. Create test fixtures.

   `cmd/rimsky-docs-lint/testdata/vocabulary-good/clean.md`:
   ```markdown
   This file uses template_hash and instance_key correctly.
   ```

   `cmd/rimsky-docs-lint/testdata/vocabulary-bad/dirty.md`:
   ```markdown
   This file accidentally references template_id and consumer_key.
   It also mentions substrate as a synonym for store.
   ```

   `cmd/rimsky-docs-lint/testdata/vocabulary-config/.vocabulary-lint.yml`:
   ```yaml
   forbidden:
     - term: '\btemplate_id\b'
       replacement: template_hash
       scope:
         - "*.md"
     - term: '\bconsumer_key\b'
       replacement: instance_key
       scope:
         - "*.md"
     - term: '\bsubstrate\b'
       replacement: store
       scope:
         - "*.md"
   ```

4. Create `vocabulary_test.go`:

   ```go
   package main

   import (
       "strings"
       "testing"
   )

   func TestVocabulary_CleanPasses(t *testing.T) {
       err := runVocabulary([]string{
           "-config=testdata/vocabulary-config/.vocabulary-lint.yml",
           "-repo-root=testdata/vocabulary-good",
       })
       if err != nil {
           t.Errorf("expected pass, got %v", err)
       }
   }

   func TestVocabulary_DirtyFails(t *testing.T) {
       err := runVocabulary([]string{
           "-config=testdata/vocabulary-config/.vocabulary-lint.yml",
           "-repo-root=testdata/vocabulary-bad",
       })
       if err == nil {
           t.Fatal("expected failure")
       }
       msg := err.Error()
       if !strings.Contains(msg, "template_id") || !strings.Contains(msg, "consumer_key") || !strings.Contains(msg, "substrate") {
           t.Errorf("expected all three terms flagged, got %s", msg)
       }
   }
   ```

**Verification:**

```sh
go test ./cmd/rimsky-docs-lint/
```

---

## Task 8 — Implement `citation-drift` subcommand

**Files:** `cmd/rimsky-docs-lint/citation_drift.go`, `cmd/rimsky-docs-lint/citation_drift_test.go`, fixtures.

**Steps:**

1. Create `citation_drift.go`:

   ```go
   package main

   import (
       "bufio"
       "bytes"
       "flag"
       "fmt"
       "os"
       "path/filepath"
       "regexp"
       "strings"

       "gopkg.in/yaml.v3"
   )

   var citationCommentRE = regexp.MustCompile(`<!--\s*@source:\s*(concepts/[a-z0-9-]+\.md)\s*-->`)

   type defOnlyFM struct {
       Definition string `yaml:"definition"`
   }

   func runCitationDrift(args []string) error {
       fs := flag.NewFlagSet("citation-drift", flag.ContinueOnError)
       publicSurface := fs.String("scope", "docs/concepts,docs/protocols,docs/humans", "comma-separated public-surface roots")
       conceptsDir := fs.String("concepts-dir", "docs/concepts", "path to concept files (citation targets)")
       if err := fs.Parse(args); err != nil {
           return err
       }
       defs, err := loadConceptDefinitions(*conceptsDir)
       if err != nil {
           return err
       }
       var hits []string
       for _, root := range strings.Split(*publicSurface, ",") {
           root = strings.TrimSpace(root)
           if root == "" {
               continue
           }
           err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
               if err != nil {
                   if os.IsNotExist(err) {
                       return nil
                   }
                   return err
               }
               if info.IsDir() || !strings.HasSuffix(path, ".md") {
                   return nil
               }
               fileHits, err := scanCitations(path, defs)
               if err != nil {
                   return err
               }
               hits = append(hits, fileHits...)
               return nil
           })
           if err != nil {
               return err
           }
       }
       if len(hits) > 0 {
           return fmt.Errorf("citation drift detected:\n  - %s", strings.Join(hits, "\n  - "))
       }
       return nil
   }

   func loadConceptDefinitions(dir string) (map[string]string, error) {
       defs := map[string]string{}
       entries, err := os.ReadDir(dir)
       if err != nil {
           return nil, err
       }
       for _, e := range entries {
           if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "README.md" {
               continue
           }
           path := filepath.Join(dir, e.Name())
           raw, err := os.ReadFile(path)
           if err != nil {
               return nil, err
           }
           if !bytes.HasPrefix(raw, []byte("---\n")) {
               continue
           }
           end := bytes.Index(raw[4:], []byte("\n---\n"))
           if end < 0 {
               continue
           }
           fm := &defOnlyFM{}
           if err := yaml.Unmarshal(raw[4:4+end], fm); err != nil {
               return nil, fmt.Errorf("%s: %w", path, err)
           }
           defs["concepts/"+e.Name()] = normalizeWhitespace(fm.Definition)
       }
       return defs, nil
   }

   func normalizeWhitespace(s string) string {
       return strings.Join(strings.Fields(s), " ")
   }

   func scanCitations(path string, defs map[string]string) ([]string, error) {
       f, err := os.Open(path)
       if err != nil {
           return nil, err
       }
       defer f.Close()
       scanner := bufio.NewScanner(f)
       scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
       lines := []string{}
       for scanner.Scan() {
           lines = append(lines, scanner.Text())
       }
       if err := scanner.Err(); err != nil {
           return nil, err
       }
       var hits []string
       for i := 0; i < len(lines); i++ {
           m := citationCommentRE.FindStringSubmatch(lines[i])
           if m == nil {
               continue
           }
           targetKey := m[1]
           wantDef, ok := defs[targetKey]
           if !ok {
               hits = append(hits, fmt.Sprintf("%s:%d cites missing target %s", path, i+1, targetKey))
               continue
           }
           // Look for the next blockquote starting the next non-blank line.
           j := i + 1
           for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
               j++
           }
           if j >= len(lines) || !strings.HasPrefix(lines[j], "> ") {
               hits = append(hits, fmt.Sprintf("%s:%d citation must be immediately followed by a markdown blockquote starting with `> `", path, i+1))
               continue
           }
           // Collect blockquote lines until non-`>` line.
           var bq strings.Builder
           for ; j < len(lines) && strings.HasPrefix(lines[j], "> "); j++ {
               bq.WriteString(strings.TrimPrefix(lines[j], "> "))
               bq.WriteByte(' ')
           }
           gotDef := normalizeWhitespace(bq.String())
           if gotDef != wantDef {
               hits = append(hits, fmt.Sprintf("%s:%d citation drift; blockquote text does not match %s `definition` frontmatter\n      got:  %q\n      want: %q",
                   path, i+1, targetKey, gotDef, wantDef))
           }
       }
       return hits, nil
   }
   ```

2. Add fixtures.

   `cmd/rimsky-docs-lint/testdata/citation-good/concepts/example.md`:
   ```markdown
   ---
   concept: example
   definition: |
     A canonical fixture definition that all citations must match.
   proto_symbol: (none)
   config_field: (none)
   api_surface: (none)
   related: []
   deprecated_terms: []
   ---

   # Example

   ## Definition

   A canonical fixture definition that all citations must match.
   ```

   `cmd/rimsky-docs-lint/testdata/citation-good/protocols/uses-it.md`:
   ```markdown
   # Uses It

   <!-- @source: concepts/example.md -->
   > A canonical fixture definition that all citations must match.

   More prose follows.
   ```

   `cmd/rimsky-docs-lint/testdata/citation-bad/concepts/example.md` — same as good.

   `cmd/rimsky-docs-lint/testdata/citation-bad/protocols/drifted.md`:
   ```markdown
   # Drifted

   <!-- @source: concepts/example.md -->
   > A different definition that has drifted from the canonical.
   ```

   `cmd/rimsky-docs-lint/testdata/citation-no-blockquote/concepts/example.md` — same as good.

   `cmd/rimsky-docs-lint/testdata/citation-no-blockquote/protocols/missing-bq.md`:
   ```markdown
   # Missing blockquote

   <!-- @source: concepts/example.md -->
   This paragraph should be a blockquote but isn't.
   ```

3. Create `citation_drift_test.go`:

   ```go
   package main

   import (
       "strings"
       "testing"
   )

   func TestCitationDrift_GoodPasses(t *testing.T) {
       err := runCitationDrift([]string{
           "-scope=testdata/citation-good/protocols",
           "-concepts-dir=testdata/citation-good/concepts",
       })
       if err != nil {
           t.Errorf("expected pass, got %v", err)
       }
   }

   func TestCitationDrift_DriftFails(t *testing.T) {
       err := runCitationDrift([]string{
           "-scope=testdata/citation-bad/protocols",
           "-concepts-dir=testdata/citation-bad/concepts",
       })
       if err == nil {
           t.Fatal("expected failure")
       }
       if !strings.Contains(err.Error(), "drift") {
           t.Errorf("expected drift in error, got %v", err)
       }
   }

   func TestCitationDrift_MissingBlockquoteFails(t *testing.T) {
       err := runCitationDrift([]string{
           "-scope=testdata/citation-no-blockquote/protocols",
           "-concepts-dir=testdata/citation-no-blockquote/concepts",
       })
       if err == nil {
           t.Fatal("expected failure")
       }
       if !strings.Contains(err.Error(), "blockquote") {
           t.Errorf("expected blockquote in error, got %v", err)
       }
   }
   ```

**Verification:**

```sh
go test ./cmd/rimsky-docs-lint/
```

---

## Task 9 — Implement `public-anchor-validity` subcommand

**Files:** `cmd/rimsky-docs-lint/public_anchor_validity.go`, `cmd/rimsky-docs-lint/public_anchor_validity_test.go`, fixtures.

**Steps:**

1. Create `public_anchor_validity.go`:

   ```go
   package main

   import (
       "bytes"
       "flag"
       "fmt"
       "os"
       "path/filepath"
       "regexp"
       "strings"

       "gopkg.in/yaml.v3"
   )

   var (
       protoMessageRE = regexp.MustCompile(`(?m)^\s*(?:message|enum)\s+([A-Za-z_][A-Za-z0-9_]*)`)
       configFieldRE  = regexp.MustCompile(`^rimsky\.yml:[A-Za-z_][A-Za-z0-9_.\[\]]*$`)
       // Real rimsky control-api routes contain underscores
       // (e.g. `/worker_requests/{id}/trace`, `/admin/scheduled-nodes/{node_id}/force-fire`).
       apiSurfaceRE = regexp.MustCompile(`^(GET|POST|PUT|DELETE|PATCH)\s+/[A-Za-z0-9_/\-{}]*$`)
   )

   type anchorFM struct {
       ProtoSymbol string `yaml:"proto_symbol"`
       ConfigField  string `yaml:"config_field"`
       APISurface   string `yaml:"api_surface"`
   }

   func runPublicAnchorValidity(args []string) error {
       fs := flag.NewFlagSet("public-anchor-validity", flag.ContinueOnError)
       conceptsDir := fs.String("concepts-dir", "docs/concepts", "path to concept files")
       protoDir := fs.String("proto-dir", "protocols/proto/v1", "path to proto sources")
       if err := fs.Parse(args); err != nil {
           return err
       }
       protoSyms, err := collectProtoSymbols(*protoDir)
       if err != nil {
           return err
       }
       entries, err := os.ReadDir(*conceptsDir)
       if err != nil {
           return err
       }
       var hits []string
       for _, e := range entries {
           if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "README.md" {
               continue
           }
           path := filepath.Join(*conceptsDir, e.Name())
           raw, err := os.ReadFile(path)
           if err != nil {
               return err
           }
           if !bytes.HasPrefix(raw, []byte("---\n")) {
               continue
           }
           end := bytes.Index(raw[4:], []byte("\n---\n"))
           if end < 0 {
               continue
           }
           fm := &anchorFM{}
           if err := yaml.Unmarshal(raw[4:4+end], fm); err != nil {
               continue
           }
           if fm.ProtoSymbol != "(none)" {
               // Expected shape: `<Name> in protocols/proto/v1/<file>.proto`
               parts := strings.SplitN(fm.ProtoSymbol, " in ", 2)
               if len(parts) != 2 {
                   hits = append(hits, fmt.Sprintf("%s: proto_symbol %q does not match shape `<Name> in protocols/proto/v1/<file>.proto`", path, fm.ProtoSymbol))
               } else {
                   name := strings.TrimSpace(parts[0])
                   if _, ok := protoSyms[name]; !ok {
                       hits = append(hits, fmt.Sprintf("%s: proto_symbol references unknown proto symbol %q (no `message %s` or `enum %s` found in %s)", path, name, name, name, *protoDir))
                   }
               }
           }
           if fm.ConfigField != "(none)" && !configFieldRE.MatchString(fm.ConfigField) {
               hits = append(hits, fmt.Sprintf("%s: config_field %q does not match shape `rimsky.yml:<dotted.path>`", path, fm.ConfigField))
           }
           if fm.APISurface != "(none)" && !apiSurfaceRE.MatchString(fm.APISurface) {
               hits = append(hits, fmt.Sprintf("%s: api_surface %q does not match shape `<HTTP_VERB> /<path>`", path, fm.APISurface))
           }
       }
       if len(hits) > 0 {
           return fmt.Errorf("public-anchor validity failed:\n  - %s", strings.Join(hits, "\n  - "))
       }
       return nil
   }

   func collectProtoSymbols(dir string) (map[string]struct{}, error) {
       symbols := map[string]struct{}{}
       err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
           if err != nil {
               if os.IsNotExist(err) {
                   return nil
               }
               return err
           }
           if info.IsDir() || !strings.HasSuffix(path, ".proto") {
               return nil
           }
           raw, err := os.ReadFile(path)
           if err != nil {
               return err
           }
           for _, m := range protoMessageRE.FindAllSubmatch(raw, -1) {
               symbols[string(m[1])] = struct{}{}
           }
           return nil
       })
       if err != nil {
           return nil, err
       }
       return symbols, nil
   }
   ```

2. Add fixtures.

   `cmd/rimsky-docs-lint/testdata/anchor-good/concepts/example.md`:
   ```markdown
   ---
   concept: example
   definition: |
     Anchor good fixture.
   proto_symbol: ExampleMsg in protocols/proto/v1/example.proto
   config_field: rimsky.yml:foo.bar
   api_surface: GET /examples/{id}
   related: []
   deprecated_terms: []
   ---
   ```

   `cmd/rimsky-docs-lint/testdata/anchor-good/proto/example.proto`:
   ```
   syntax = "proto3";
   package example.v1;
   message ExampleMsg {
     string id = 1;
   }
   ```

   `cmd/rimsky-docs-lint/testdata/anchor-bad/concepts/example.md`:
   ```markdown
   ---
   concept: example
   definition: |
     Anchor bad fixture.
   proto_symbol: NonexistentMsg in protocols/proto/v1/example.proto
   config_field: rimsky.yml:foo.bar
   api_surface: GET /ok
   related: []
   deprecated_terms: []
   ---
   ```

   `cmd/rimsky-docs-lint/testdata/anchor-bad/proto/example.proto`:
   ```
   syntax = "proto3";
   package example.v1;
   message ExampleMsg {
     string id = 1;
   }
   ```

3. Create `public_anchor_validity_test.go`:

   ```go
   package main

   import (
       "strings"
       "testing"
   )

   func TestPublicAnchorValidity_GoodPasses(t *testing.T) {
       err := runPublicAnchorValidity([]string{
           "-concepts-dir=testdata/anchor-good/concepts",
           "-proto-dir=testdata/anchor-good/proto",
       })
       if err != nil {
           t.Errorf("expected pass, got %v", err)
       }
   }

   func TestPublicAnchorValidity_BadFails(t *testing.T) {
       err := runPublicAnchorValidity([]string{
           "-concepts-dir=testdata/anchor-bad/concepts",
           "-proto-dir=testdata/anchor-bad/proto",
       })
       if err == nil {
           t.Fatal("expected failure")
       }
       if !strings.Contains(err.Error(), "NonexistentMsg") {
           t.Errorf("expected NonexistentMsg in error, got %v", err)
       }
   }
   ```

**Verification:**

```sh
go test ./cmd/rimsky-docs-lint/
```

---

## Task 10 — Implement `llms-txt-validity` subcommand

**Files:** `cmd/rimsky-docs-lint/llms_txt_validity.go`, `cmd/rimsky-docs-lint/llms_txt_validity_test.go`, fixtures.

**Steps:**

1. Create `llms_txt_validity.go`:

   ```go
   package main

   import (
       "bytes"
       "flag"
       "fmt"
       "os"
       "path/filepath"
       "regexp"
       "strings"
   )

   // Markdown link target capturing group: matches [text](url) and (url) only.
   var markdownLinkRE = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)

   func runLLMSTxtValidity(args []string) error {
       fs := flag.NewFlagSet("llms-txt-validity", flag.ContinueOnError)
       llmsTxt := fs.String("llms-txt", "docs/agents/llms.txt", "path to llms.txt")
       llmsFull := fs.String("llms-full", "docs/agents/llms-full.txt", "path to llms-full.txt")
       repoRoot := fs.String("repo-root", ".", "repo root for link resolution")
       rootLLMSTxt := fs.String("root-llms-txt", "llms.txt", "repo-root copy")
       rootLLMSFull := fs.String("root-llms-full", "llms-full.txt", "repo-root copy")
       if err := fs.Parse(args); err != nil {
           return err
       }
       var hits []string
       hits = append(hits, validateLLMSTxtShape(*llmsTxt, *repoRoot, "docs/agents")...)
       hits = append(hits, validateLLMSTxtShape(*llmsFull, *repoRoot, "docs/agents")...)
       hits = append(hits, validateRootCopy(*llmsTxt, *rootLLMSTxt)...)
       hits = append(hits, validateRootCopy(*llmsFull, *rootLLMSFull)...)
       if len(hits) > 0 {
           return fmt.Errorf("llms-txt-validity failed:\n  - %s", strings.Join(hits, "\n  - "))
       }
       return nil
   }

   func validateLLMSTxtShape(path, repoRoot, baseDir string) []string {
       var hits []string
       raw, err := os.ReadFile(path)
       if err != nil {
           return []string{fmt.Sprintf("%s: %v", path, err)}
       }
       lines := strings.Split(string(raw), "\n")
       if len(lines) < 2 || !strings.HasPrefix(lines[0], "# ") {
           hits = append(hits, fmt.Sprintf("%s: must start with `# <Title>`", path))
       }
       sawDescription := false
       for i := 1; i < len(lines) && i < 6; i++ {
           if strings.HasPrefix(lines[i], "> ") {
               sawDescription = true
               break
           }
       }
       if !sawDescription {
           hits = append(hits, fmt.Sprintf("%s: must contain a `> <description>` blockquote near the top", path))
       }
       for i, line := range lines {
           for _, m := range markdownLinkRE.FindAllStringSubmatch(line, -1) {
               url := m[1]
               if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
                   continue
               }
               // Resolve relative URL against the file's directory.
               base := filepath.Dir(path)
               candidate := filepath.Join(base, url)
               if _, err := os.Stat(candidate); err != nil {
                   // also try resolving from repoRoot
                   alt := filepath.Join(repoRoot, url)
                   if _, err := os.Stat(alt); err != nil {
                       hits = append(hits, fmt.Sprintf("%s:%d link target does not resolve: %s", path, i+1, url))
                   }
               }
           }
       }
       _ = baseDir
       return hits
   }

   func validateRootCopy(canonical, rootCopy string) []string {
       canon, err := os.ReadFile(canonical)
       if err != nil {
           return []string{fmt.Sprintf("%s: %v", canonical, err)}
       }
       got, err := os.ReadFile(rootCopy)
       if err != nil {
           return []string{fmt.Sprintf("%s: %v", rootCopy, err)}
       }
       if !bytes.Equal(canon, got) {
           return []string{fmt.Sprintf("%s does not byte-equal %s; run `make docs-roots` to update", rootCopy, canonical)}
       }
       return nil
   }
   ```

2. Add fixtures.

   `cmd/rimsky-docs-lint/testdata/llms-good/docs/agents/llms.txt`:
   ```
   # Test

   > Test description.

   ## Concepts

   - [Example](concepts/example.md): A test concept.
   ```

   `cmd/rimsky-docs-lint/testdata/llms-good/docs/agents/llms-full.txt`:
   ```
   # Full

   > Full content for testing.

   Body text.
   ```

   `cmd/rimsky-docs-lint/testdata/llms-good/docs/concepts/example.md`:
   ```markdown
   # Example
   ```

   `cmd/rimsky-docs-lint/testdata/llms-good/llms.txt` — byte-equal copy of `docs/agents/llms.txt` above.

   `cmd/rimsky-docs-lint/testdata/llms-good/llms-full.txt` — byte-equal copy.

   `cmd/rimsky-docs-lint/testdata/llms-bad-broken-link/docs/agents/llms.txt`:
   ```
   # Test

   > Test description.

   - [Missing](concepts/does-not-exist.md): broken.
   ```

   With matching `docs/agents/llms-full.txt`, `llms.txt`, `llms-full.txt` minimal versions and no concepts/ directory.

3. Create `llms_txt_validity_test.go`:

   ```go
   package main

   import (
       "strings"
       "testing"
   )

   func TestLLMSTxtValidity_GoodPasses(t *testing.T) {
       err := runLLMSTxtValidity([]string{
           "-llms-txt=testdata/llms-good/docs/agents/llms.txt",
           "-llms-full=testdata/llms-good/docs/agents/llms-full.txt",
           "-repo-root=testdata/llms-good",
           "-root-llms-txt=testdata/llms-good/llms.txt",
           "-root-llms-full=testdata/llms-good/llms-full.txt",
       })
       if err != nil {
           t.Errorf("expected pass, got %v", err)
       }
   }

   func TestLLMSTxtValidity_BrokenLinkFails(t *testing.T) {
       err := runLLMSTxtValidity([]string{
           "-llms-txt=testdata/llms-bad-broken-link/docs/agents/llms.txt",
           "-llms-full=testdata/llms-bad-broken-link/docs/agents/llms-full.txt",
           "-repo-root=testdata/llms-bad-broken-link",
           "-root-llms-txt=testdata/llms-bad-broken-link/llms.txt",
           "-root-llms-full=testdata/llms-bad-broken-link/llms-full.txt",
       })
       if err == nil {
           t.Fatal("expected failure")
       }
       if !strings.Contains(err.Error(), "does-not-exist.md") {
           t.Errorf("expected broken link in error, got %v", err)
       }
   }
   ```

**Verification:**

```sh
go test ./cmd/rimsky-docs-lint/
```

---

## Task 11 — Add `make docs-roots` target

**Files:** `Makefile`.

**Steps:**

1. Append to Makefile:

   ```makefile
   docs-roots: docs-llms-full
   	cp docs/agents/llms.txt llms.txt
   	cp docs/agents/llms-full.txt llms-full.txt

   docs-build: docs-glossary docs-llms-full docs-roots
   ```

   Add `docs-roots docs-build` to `.PHONY`.

**Verification:**

```sh
# Smoke: target exists and depends on docs-llms-full.
grep -q "^docs-roots:" Makefile
grep -q "^docs-build:" Makefile
```

---

## Task 12 — Create directory skeleton + `docs/README.md`

**Files:** `docs/README.md`, plus empty placeholder concept directories: `docs/concepts/`, `docs/protocols/`, `docs/agents/errors/`, `docs/agents/examples/`, `docs/humans/`.

**Steps:**

1. Create the directories. macOS / Linux:

   ```sh
   mkdir -p docs/concepts docs/protocols docs/agents/errors docs/agents/examples docs/humans
   ```

2. Create `docs/README.md`. The doc-tree map must distinguish public from internal/working:

   ```markdown
   # Rimsky documentation

   This directory hosts both the **public-documentation surface** (intended for external consumers and their coding agents) and the **internal/working surface** (engineering material that is unmaintained going forward and not intended for external citation).

   ## Public surface

   - `concepts/` — canonical per-concept reference (one file per domain noun).
   - `protocols/` — protocol-implementation guides (`ClaimProducer`, `Executor`, `LifecycleSubscriber`).
   - `agents/` — agent-shaped indices (`llms.txt`, `llms-full.txt`), error catalog, copy-pasteable examples.
   - `humans/` — thin human-shaped surface (landing, narrative concept walk, dashboard guide).
   - `glossary.md` — generated from `concepts/`. Do not hand-edit.
   - `vocabulary.md` — deprecated terms, layered-sense disambiguation.
   - `licensing.md` — repo licensing notice.

   ## Working / internal surface

   - `internal/` — working engineering reference. **Unmaintained.** Not cited by the public surface.
   - `specs/`, `plans/`, `history/`, `future-work/` — pipeline artifacts (specs, implementation plans, archived design docs). Ephemeral.
   - `examples/` — narrative case-making material; not yet promoted to the public surface.

   The public surface is fully self-contained: it cites within itself and into `protocols/proto/v1/*.proto` (the public wire contract). It does not cite, link to, or reference any file under `internal/`, `specs/`, `plans/`, `history/`, `future-work/`, or `examples/`.
   ```

**Verification:**

```sh
test -d docs/concepts && test -d docs/protocols && test -d docs/agents/errors && test -d docs/agents/examples && test -d docs/humans && test -f docs/README.md && echo OK
```

---

## Task 13 — Create `docs/vocabulary.md`

**Files:** `docs/vocabulary.md`.

**Steps:**

1. Create `docs/vocabulary.md`:

   ```markdown
   # Rimsky public-surface vocabulary discipline

   Three rules govern naming on the public-documentation surface.

   ## 1. One concept, one name

   Every concept has exactly one canonical name. Synonyms are forbidden. Where historical synonyms exist, they are listed below as deprecated, with the current term and the rationale for the change.

   ### Deprecated terms

   | Deprecated | Current | Rationale |
   |---|---|---|
   | `template_id` | `template_hash` | Templates are content-addressed; `_hash` makes the addressing scheme explicit. |
   | `consumer_key` | `instance_key` | The optional dedup key on an instance is an instance-level concept, not consumer-level. |
   | `substrate` | `store` (bundled-services-layer) or `claim producer` (protocol-layer) per context | "Substrate" conflated the underlying physical storage with the rimsky-side service that wraps it. |

   The grep-enforced subset of this list lives at `.vocabulary-lint.yml` (run `go run ./cmd/rimsky-docs-lint vocabulary` to check). Additional forbidden terms get added as concept-file fill surfaces them; each addition specifies a concrete grep pattern.

   ## 2. One name, one concept (with layered-senses disambiguation)

   Some Rimsky terms have layered senses — same word, slightly different presentation per layer of the four-layer model:

   - **"Store"** is *not used* at the protocol layer (use "claim producer"). At the bundled-services layer, "store" is the colloquial name for a data-backed claim producer (filesystem store, postgres store, stub store).
   - **"Frame"** appears at the foundation layer (frame-id correlation) and the modeling layer (the unit of cascade resolution).
   - **"Claim"** is a foundation-layer primitive with a modeling-layer presentation in templates.

   Layered terms are documented in the `layer_senses` frontmatter entry and "Layer senses" prose section of the relevant `concepts/<term>.md` file. They are *not* enforced via grep — they are context-dependent.

   Where a Rimsky term overlaps with general programming vocabulary ("frame," "cascade," "claim," "store," "region"), the concept file's "Common mistakes" section disambiguates the Rimsky meaning from neighboring meanings (stack frame, CSS cascade, JWT claim, Redux store, AWS region).

   ## 3. Anchors

   Every canonical concept file declares up to three anchors in its frontmatter:

   - `proto_symbol` — the proto symbol that carries the concept on the wire (under `protocols/proto/v1/`). `(none)` if the concept does not appear on the wire.
   - `config_field` — the path inside `rimsky.yml` (operator config) where the concept surfaces. `(none)` if not.
   - `api_surface` — the control-api HTTP route where the concept surfaces. `(none)` if not.

   Internal anchors (Go types, SQL tables, blessed-invariant numbers) are deliberately *not* part of the public-surface vocabulary. Consumer-visible properties go in the prose "Consumer-visible guarantees" section of the relevant concept file.
   ```

**Verification:**

```sh
test -f docs/vocabulary.md && echo OK
go run ./cmd/rimsky-docs-lint vocabulary
```

The vocabulary lint MUST pass (no forbidden terms in the surface so far).

---

## Task 14 — Create `docs/internal/README.md` (unmaintained notice)

**Files:** `docs/internal/README.md`.

**Steps:**

1. Create `docs/internal/README.md`:

   ```markdown
   # docs/internal/ — UNMAINTAINED

   Everything in this directory is internal/working engineering material. It is **unmaintained going forward** and is **not** part of the public-documentation surface.

   For canonical user-facing material, see the public surface above this directory:

   - `../concepts/` — canonical per-concept reference.
   - `../protocols/` — protocol-implementation guides.
   - `../agents/` — agent-shaped indices.
   - `../humans/` — narrative human-shaped onboarding.
   - `../glossary.md`, `../vocabulary.md` — public vocabulary.

   Do **not** cite or link to files in this directory from the public surface.
   ```

**Verification:**

```sh
test -f docs/internal/README.md && echo OK
```

---

## Task 15 — Create concept-file production checklist

This task does not produce a file; it captures the per-concept checklist used by Task 16 (the actual concept-file authoring). Read it once and refer back to it.

**Per-concept production checklist:**

For each concept file the implementer creates in Task 16:

1. Use the per-concept-file shape from spec §3 verbatim (frontmatter + Definition / Why it exists / Layer senses / How you encounter it / Consumer-visible guarantees / Common mistakes / See also).
2. Frontmatter requirements:
   - `concept`: kebab-case, matches filename without `.md`.
   - `definition`: 1-3 sentences. Lift from `docs/internal/glossary.md`'s entry for the term, restructure for the public surface (no SQL table refs, no internal-invariant numbers, no Go-package refs).
   - `proto_symbol`: shape `<Name> in protocols/proto/v1/<file>.proto`, or literal `(none)`. The lint (Task 9) enforces this. Look in the `.proto` files to confirm message names.
   - `config_field`: shape `rimsky.yml:<dotted.path>`, or `(none)`. Cross-check against `deploy/rimsky.yml`.
   - `api_surface`: shape `<HTTP_VERB> /<path>`, or `(none)`. Cross-check against control-api routes (search `modeling/controlapi/` for handler registrations).
   - `related`: list of related concept names (kebab-case). May be empty `[]`.
   - `deprecated_terms`: list of synonyms historically used for this concept. Most are `[]`. Specific entries: `claim-producer.md` carries `[Store, StoreService, Bridge]`; `instance.md` carries `[consumer_key]` if it explicitly names the deprecation; `template.md` carries `[template_id]`; `scope.md` carries `[region]`. Do not re-list terms in the seed `.vocabulary-lint.yml` (that file is the grep-enforcement; this list is for the glossary "Deprecated terms" section).
   - `layer_senses`: omitted entirely if not applicable. Where applicable, each entry has `layer:` (one of `foundation | modeling | protocol | bundled-services`) and `sense:` (one-sentence presentation).
3. Body sections:
   - **Definition** — 1-3 sentences, identical text to frontmatter `definition` (the redundancy is intentional for retrieval).
   - **Why it exists** — 2-4 paragraphs. Lift narrative from `docs/internal/node-graph-design.md`, `docs/specs/2026-05-04-foundation-contract.md`, `docs/specs/2026-05-04-modeling-layer-contract.md`, `docs/specs/2026-05-04-service-protocol-contract.md` as appropriate. Restructure to drop internal package paths and SQL-table names.
   - **Layer senses** — only when frontmatter `layer_senses` is non-empty. One paragraph per sense.
   - **How you encounter it** — concrete consumer surfaces. Config field path, API endpoint, dashboard view, wire message, CLI command. For internal-to-rimsky concepts (e.g. `frame`, `holding-subgraph`), state explicitly: *"Not directly observable to consumers. Documented here because cascade resolution depends on it."*
   - **Consumer-visible guarantees** — only when relevant. Properties consumers can rely on. Translate consumer-visible blessed invariants into plain prose (do **not** cite invariant numbers; the public surface does not couple to source-code annotations).
   - **Common mistakes** — anti-patterns and disambiguation. Mandatory section. Include at least one disambiguation entry per concept (Rimsky's frame ≠ stack frame; Rimsky's cascade ≠ CSS cascade; Rimsky's store ≠ Redux store; etc.). Each entry is one sentence stating the mistake, one sentence stating the correct behavior.
   - **See also** — bullet list of related concept files (relative links).
4. After authoring each file, run:

   ```sh
   go run ./cmd/rimsky-docs-lint frontmatter -dir=docs/concepts
   go run ./cmd/rimsky-docs-lint public-anchor-validity -concepts-dir=docs/concepts -proto-dir=protocols/proto/v1
   ```

   Both must exit 0 before proceeding to the next concept.

5. The final concept file's commit must round-trip through the glossary generator without diff:

   ```sh
   go run ./cmd/rimsky-docs-glossary
   git diff --exit-code docs/glossary.md
   ```

**Verification:** None — this task is a checklist. Task 16 carries the verification.

---

## Task 16 — Author the 23 concept files

**Files:** all under `docs/concepts/`.

The canonical list:

| # | File | Layer-senses? | Lift sources |
|---|---|---|---|
| 1 | `four-layer-model.md` | n/a | spec §1, `docs/internal/glossary.md` "Four-layer model" section |
| 2 | `node.md` | foundation+modeling | `docs/internal/node-graph-design.md` (node section), `docs/specs/2026-05-04-foundation-contract.md` (cascade), `docs/specs/2026-05-04-modeling-layer-contract.md` (presentation) |
| 3 | `node-state.md` | foundation+modeling | `docs/internal/glossary.md` "Public state vocabulary", `docs/internal/node-graph-design.md` |
| 4 | `cascade.md` | none | `docs/internal/node-graph-design.md` (cascade section), `docs/specs/2026-05-04-foundation-contract.md` |
| 5 | `invalidate.md` | none | `docs/internal/glossary.md` "Public message vocabulary", `docs/internal/node-graph-design.md` |
| 6 | `recalculate.md` | none | same as invalidate |
| 7 | `frame.md` | foundation+modeling | `docs/internal/glossary.md` "Frame & scheduling", `docs/specs/2026-05-04-modeling-layer-contract.md` (frame-resolution) |
| 8 | `frame-resolution.md` | none | `docs/specs/2026-05-04-modeling-layer-contract.md` |
| 9 | `claim.md` | foundation+modeling | `docs/internal/glossary.md` (Core vocabulary), `docs/specs/2026-05-04-foundation-contract.md`. Inline-document `alias`, `intent`, `address`, `payload` as sub-properties. |
| 10 | `claim-handle.md` | foundation | `docs/internal/glossary.md` "Claim handle", `docs/specs/2026-05-04-foundation-contract.md` |
| 11 | `scope.md` | foundation | `docs/internal/glossary.md` "Scope" + "Selector". Document `selector` inline. |
| 12 | `named-lock.md` | foundation | `docs/internal/glossary.md` "Named lock" |
| 13 | `write-semantics.md` | none | `docs/internal/glossary.md` "Write semantics" section. Inline-document `WriteSemanticsEnvelope` and `realized_write_semantics`. |
| 14 | `holding-subgraph.md` | foundation+modeling | `docs/internal/glossary.md` "Lifecycle & propagation" (holding subgraph), `docs/specs/2026-05-04-foundation-contract.md` (auto-terminal) |
| 15 | `inheritance.md` | modeling | `docs/internal/glossary.md` "Inheritance" + "Value-pass" + "Claim-pass". Document propagation modes inline. |
| 16 | `template.md` | modeling | `docs/internal/glossary.md` "Control-plane v1 vocabulary" (Template entry), `docs/specs/2026-05-04-modeling-layer-contract.md` |
| 17 | `instance.md` | modeling | `docs/internal/glossary.md` (Instance entry), `docs/specs/2026-05-04-modeling-layer-contract.md` |
| 18 | `tag.md` | modeling | `docs/internal/glossary.md` (Tag entry) |
| 19 | `attributes.md` | modeling | `docs/internal/node-graph-design.md` (attributes section), `docs/specs/2026-05-04-modeling-layer-contract.md` |
| 20 | `userdata.md` | modeling | `docs/internal/node-graph-design.md` (userdata section). Consumer-visible guarantee: rimsky never inspects, parses, substitutes, or validates `userdata` — the executor receives the bytes verbatim. |
| 21 | `claim-producer.md` | protocol+bundled-services | `docs/internal/glossary.md` "ClaimProducer" + "Things deliberately NOT in the vocabulary" (Store entry). The bundled-services-layer sense names "store" as the colloquialism. Inline-document the five verbs (`Open`, `Commit`, `Abandon`, `Release`, `Capabilities`). |
| 22 | `executor.md` | protocol | `docs/internal/glossary.md` "Executor", `docs/internal/executor-author-guide.md` (top sections). Inline-document the four methods. |
| 23 | `lifecycle-subscriber.md` | protocol | `docs/internal/glossary.md` "LifecycleSubscriber", `docs/specs/2026-05-04-modeling-layer-contract.md` (lifecycle events section). Inline-document the six events. |

For each entry, follow the per-concept production checklist in Task 15.

**Steps:**

1. Author the 23 files in the order above. The order is dependency-respectful (foundation → coordination → modeling → protocols), so each file may safely cite earlier ones via the `<!-- @source: ... -->` convention if it carries definition-shaped text from another concept.

2. **Required disambiguations in "Common mistakes" sections.** At minimum:
   - `frame.md` — Rimsky's frame ≠ stack frame, video frame, UI frame.
   - `cascade.md` — Rimsky's cascade ≠ CSS cascade.
   - `claim.md` — Rimsky's claim ≠ JWT claim, insurance claim.
   - `claim-producer.md` — Rimsky's "store" (the bundled-services colloquialism) ≠ Redux store, Vue store, Svelte store. Also: `Store` (the protocol-layer interface name) is gone — use `ClaimProducer`.
   - `scope.md` — Rimsky's scope ≠ JavaScript variable scope ≠ AWS resource scope.
   - `node.md` — Rimsky's node ≠ Node.js, Kubernetes node, network node.
   - `tag.md` — Rimsky's tag ≠ git tag, HTML tag, container image tag (note the analogy is close to image tags but distinct: tags here move atomically and reject hash-shape values).
   - `instance.md` — Rimsky's instance ≠ AWS EC2 instance ≠ class instance ≠ template instance in the C++ sense.
   - `userdata.md` — Rimsky's userdata is opaque bytes; rimsky never parses, substitutes, or inspects it. Distinct from cloud-init userdata (which IS parsed by the cloud provider).

3. After every concept file, run the per-file lint pair (frontmatter + public-anchor-validity). After the last concept file, run the full sweep:

   ```sh
   go run ./cmd/rimsky-docs-lint frontmatter -dir=docs/concepts
   go run ./cmd/rimsky-docs-lint public-anchor-validity -concepts-dir=docs/concepts -proto-dir=protocols/proto/v1
   ```

4. Create `docs/concepts/README.md`:

   ```markdown
   # Concepts

   Canonical reference for every Rimsky domain noun. One file per concept.

   For the structural overview that organizes these terms, start with [`four-layer-model.md`](four-layer-model.md).

   For the auto-generated alphabetical index, see [`../glossary.md`](../glossary.md).

   For deprecated synonyms and the discipline that keeps the vocabulary clean, see [`../vocabulary.md`](../vocabulary.md).
   ```

**Verification:**

```sh
go run ./cmd/rimsky-docs-lint frontmatter -dir=docs/concepts
go run ./cmd/rimsky-docs-lint public-anchor-validity -concepts-dir=docs/concepts -proto-dir=protocols/proto/v1
test -f docs/concepts/README.md && echo OK
ls docs/concepts/*.md | wc -l   # expect at least 24 (23 concept files + README.md)
```

All exits 0; concept count is at least 24.

---

## Task 17 — Generate `docs/glossary.md` and verify parity

**Files:** `docs/glossary.md` (generated).

**Steps:**

1. Generate:

   ```sh
   make docs-glossary
   ```

2. Verify the generated file passes the parity lint:

   ```sh
   go run ./cmd/rimsky-docs-lint glossary-parity
   ```

**Verification:**

```sh
test -f docs/glossary.md && grep -q "AUTOGENERATED" docs/glossary.md && echo OK
go run ./cmd/rimsky-docs-lint glossary-parity
```

---

## Task 18 — Author `docs/protocols/` guides

**Files:** `docs/protocols/README.md`, `docs/protocols/claim-producer.md`, `docs/protocols/executor.md`, `docs/protocols/lifecycle-subscriber.md`.

**Steps:**

1. Create `docs/protocols/README.md`:

   ```markdown
   # Protocol-implementation guides

   These guides cover the gap between *understanding the concepts* and *implementing a custom service against the wire protocol in your language of choice*.

   - [ClaimProducer](claim-producer.md) — implement the producer protocol: `Open`, `Commit`, `Abandon`, `Release`, `Capabilities`.
   - [Executor](executor.md) — implement the executor protocol: `Execute`, `StreamTrace`, `GetTrace`, `GetCapabilities`.
   - [LifecycleSubscriber](lifecycle-subscriber.md) — implement the lifecycle protocol: six template/instance state-transition hooks.

   The proto definitions are at [`protocols/proto/v1/`](../../protocols/proto/v1/). Generate language bindings with `protoc` (the rimsky build uses `make proto-gen`).
   ```

2. Create `docs/protocols/claim-producer.md` by lifting from `docs/internal/claim-producer-author-guide.md`. Restructure:
   - Replace any reference to internal Go packages (e.g. `foundation/integration/remote/`) with consumer-friendly framing ("rimsky's gRPC client").
   - Replace `@blessed-invariant N` references with plain prose statements of the property.
   - Replace `rimsky_claim_handle` and other internal SQL-table references with the appropriate concept ("the rimsky-side claim handle persists across calls; the producer should expect any of the four verbs to arrive with a previously-issued `claim_id`").
   - Where a definition-shaped paragraph (e.g. "A claim is...") appears, replace with a `<!-- @source: concepts/claim.md -->` comment + matching blockquote (per the citation-drift convention from Task 8).
   - Drop any narrative that explains why we made design choices internally; keep guidance for implementers.
   - Reference proto file paths: `protocols/proto/v1/claim_producer.proto`.

3. Create `docs/protocols/executor.md` by lifting from `docs/internal/executor-author-guide.md`. Same scrubbing discipline. Cover:
   - The four methods (`Execute`, `StreamTrace`, `GetTrace`, `GetCapabilities`).
   - The async-callback path (executor → supervisor callback URL with the `${callback_url}/v1/callback/{async_ack_id}` shape).
   - The userdata-is-opaque guarantee (`<!-- @source: concepts/userdata.md -->`).
   - The conformance binary: `cmd/rimsky-executor-conformance` and how to run it against a non-bundled executor.
   - Reference proto file paths: `protocols/proto/v1/executor.proto`.

4. Create `docs/protocols/lifecycle-subscriber.md`. New writing (no internal predecessor file). Cover:
   - The six methods: `OnTemplateRegistered`, `OnTemplateDeployed`, `OnTemplateUndeployed`, `OnTemplateDeregistered`, `OnInstanceCreated`, `OnInstanceTerminated`.
   - Idempotency: rimsky tracks idempotency by `(peer-name, event-type, object-id)`. Replays are no-ops in rimsky; peers should still write idempotent handlers.
   - Opt-in via `protocols: [..., lifecycle_subscriber]` in `rimsky.yml`. (Cite `<!-- @source: concepts/lifecycle-subscriber.md -->`.)
   - Reference proto file paths: `protocols/proto/v1/lifecycle.proto`.

5. After authoring all three, run:

   ```sh
   go run ./cmd/rimsky-docs-lint citation-drift -scope=docs/protocols -concepts-dir=docs/concepts
   go run ./cmd/rimsky-docs-lint vocabulary
   ```

**Verification:**

```sh
test -f docs/protocols/README.md && test -f docs/protocols/claim-producer.md && test -f docs/protocols/executor.md && test -f docs/protocols/lifecycle-subscriber.md && echo OK
go run ./cmd/rimsky-docs-lint citation-drift -scope=docs/protocols -concepts-dir=docs/concepts
go run ./cmd/rimsky-docs-lint vocabulary
```

---

## Task 19 — Curate the error catalog

**Files:** `docs/agents/errors/README.md`, individual error files.

**Steps:**

1. Create `docs/agents/errors/README.md`:

   ```markdown
   # Error catalog

   One file per consumer-observable error. Each file states what the error means, when it happens, and what to do.

   *Internal-correctness errors* (state-machine rejections, sweep-internal errors, advisory-lock failures) are not listed here — they are not consumer-observable.

   ## Index

   - [`orphaned_claim_lost_race.md`](orphaned_claim_lost_race.md) — supervisor lost ownership of a claim mid-execution.
   - (additional entries below as the catalog grows)
   ```

2. Identify the initial error set. Walk these sources and record each consumer-observable error string:
   - `protocols/proto/v1/*.proto` — error response messages and enums.
   - `modeling/controlapi/` — operator-facing config-validation errors emitted at startup or on `POST /...` requests.
   - `modeling/cli/` — CLI errors emitted by `rimsky-cli`.
   - `foundation/integration/runner_acquire.go` — `orphaned_claim_lost_race` is canonically here.

   Aim for ~12–15 initial entries. Group examples (the implementer expands each into a file):

   - `orphaned_claim_lost_race` — supervisor verify-before-run failed.
   - Capability-handshake mismatches — operator-declared envelope ⊄ producer-declared envelope.
   - Tag-shape rejection — caller passed a `sha256-...` shape as a tag.
   - Compose-prefix violation — caller manually registered with `compose:<project>:` prefix outside `rimsky-cli compose`.
   - Schedule-cron parse failure.
   - Template-not-deployed — instance creation against a registered-but-not-deployed template.
   - Stub-mode probe failure — `--require-stub-mode` rejected a non-stub executor.
   - Conformance failures (per the conformance binaries' error vocabulary).
   - Async-callback wrong-key error — TS executor posted with `kind:` instead of `type:` (cited from `executors/claude-agent/src/server.test.ts`).

3. For each error, create a file `docs/agents/errors/<error-name>.md` with the shape from spec §8.4:

   ```markdown
   ---
   error: orphaned_claim_lost_race
   surfaced_to: executor
   ---

   # `orphaned_claim_lost_race`

   ## What it means

   The supervisor began running a node, then re-checked claim ownership immediately before dispatching, and found another supervisor had taken over. The node was not dispatched.

   ## When it happens

   Two supervisors briefly contended for the same claim. The losing supervisor backs off; the winning supervisor proceeds. This is a normal contention outcome under multi-replica deployments — not a fault.

   ## What to do

   No action required. The claim will be re-attempted on the next scheduling tick if needed. If you see this error frequently (more than once every few seconds), check that supervisor heartbeat intervals and orphan-reaper cutoffs are configured consistently across replicas.

   ## See also

   - [`concepts/claim-handle.md`](../../concepts/claim-handle.md)
   - [`concepts/claim.md`](../../concepts/claim.md)
   ```

   Adapt the structure for each error.

**Verification:**

```sh
test -f docs/agents/errors/README.md && echo OK
ls docs/agents/errors/*.md | wc -l   # expect at least 11 (10 errors + README.md), aim for 13-16
go run ./cmd/rimsky-docs-lint vocabulary
```

---

## Task 20 — Author `docs/agents/examples/`

**Files:** `docs/agents/examples/README.md`, six example files.

**Steps:**

1. Create `docs/agents/examples/README.md`:

   ```markdown
   # Examples

   Complete, copy-pasteable, no-ellipsis examples. Each file is runnable as written. Where a precondition is required (the bundled docker-compose stack must be up, etc.), the example states the exact command at the top.

   - [`minimal-rimsky-yml.md`](minimal-rimsky-yml.md) — minimal operator config.
   - [`minimal-template-and-instance.md`](minimal-template-and-instance.md) — register a one-node template, create an instance, observe completion.
   - [`two-node-with-claim.md`](two-node-with-claim.md) — claim dependency between two nodes.
   - [`claude-agent-userdata.md`](claude-agent-userdata.md) — `userdata` substitution into a claude-agent executor.
   - [`holding-subgraph.md`](holding-subgraph.md) — held-claim resolution via `inherits:`.
   - [`rimsky-compose-multi-template.md`](rimsky-compose-multi-template.md) — multi-template project.
   ```

2. Lift `deploy/rimsky.yml` into `docs/agents/examples/minimal-rimsky-yml.md`. Drop blocks not relevant to a minimal smoke run; keep the persistence block (sqlite default for dev), one named lock, one bundled claim-producer, one bundled executor. Wrap the YAML in a fenced block. State at the top: *"Save this as `rimsky.yml` and pass `RIMSKY_CONFIG=$PWD/rimsky.yml` to each rimsky binary, or place under `/etc/rimsky/rimsky.yml`."*

3. `minimal-template-and-instance.md` — author from `test/smoke/setup.go` and `test/scenarios/lifecycle/`. Three blocks:
   - The template YAML (one node, no claims).
   - The CLI invocation: `rimsky-cli template register ...`, `rimsky-cli template deploy ...`, `rimsky-cli instance create ...`.
   - The expected control-api output for `GET /instances/{id}` after completion.

4. `two-node-with-claim.md` — Two nodes, one declares a claim on a stub claim-producer; the second node depends on the first via `deps:`. Same shape as above.

5. `claude-agent-userdata.md` — Template using `claude-agent` executor with a `userdata` block containing literal `{{...}}` (which must NOT be substituted by rimsky). Demonstrate that the executor receives the bytes verbatim. State the verification: pull the executor trace via `GET /worker_requests/{id}/trace` and observe the literal `{{...}}` in the prompt. Reference proto: `protocols/proto/v1/executor.proto`.

6. `holding-subgraph.md` — Three nodes: an acquirer, two inheritors via `inherits: [acquirer-alias]`. Demonstrate held-claim auto-terminal: when both inheritors complete (one with success, one with failure), the held claim is auto-`Abandon`ed (any-failure → Abandon).

7. `rimsky-compose-multi-template.md` — A `rimsky-compose.yml` declaring two templates and one persistent instance. Show the apply command (`rimsky-cli compose apply`).

8. Each file ends with a "Verification" block: the exact commands to run after applying the example, and the expected output.

**Verification:**

```sh
test -f docs/agents/examples/README.md && echo OK
ls docs/agents/examples/*.md | wc -l   # expect 7 (6 examples + README)
go run ./cmd/rimsky-docs-lint vocabulary
```

---

## Task 21 — Author `docs/agents/llms.txt`

**Files:** `docs/agents/llms.txt`.

**Steps:**

1. Create `docs/agents/llms.txt` per spec §8.1. Use one bullet per concept (definition lifted from each file's frontmatter), bullets for the three protocols, the six examples, and the error catalog README. Place the glossary, vocabulary, and dashboard guide under `## Optional`.

   Format (skeleton — fill the bullets from the actual frontmatter `definition` of each concept):

   ```
   # Rimsky

   > Project-agnostic reactive node-graph orchestration platform organized as four layers (foundation, modeling, service protocols, bundled services). Coding agents are the primary documentation consumer.

   ## Concepts

   - [Four-layer model](concepts/four-layer-model.md): The vocabulary structure: foundation, modeling, service protocols, bundled services.
   - [Node](concepts/node.md): <one-sentence definition from frontmatter>
   - [Frame](concepts/frame.md): <...>
   - <one bullet per concept file, sorted by concept name>

   ## Protocols

   - [ClaimProducer](protocols/claim-producer.md): How to implement a claim producer (Open / Commit / Abandon / Release / Capabilities).
   - [Executor](protocols/executor.md): How to implement an executor (Execute / StreamTrace / GetTrace / GetCapabilities).
   - [LifecycleSubscriber](protocols/lifecycle-subscriber.md): How to implement a lifecycle subscriber (six template/instance state-transition hooks).

   ## Examples

   - [Minimal rimsky.yml](agents/examples/minimal-rimsky-yml.md)
   - [Minimal template and instance](agents/examples/minimal-template-and-instance.md)
   - [Two-node template with a claim dependency](agents/examples/two-node-with-claim.md)
   - [Claude-agent template demonstrating userdata substitution](agents/examples/claude-agent-userdata.md)
   - [Holding-subgraph template demonstrating held-claim resolution](agents/examples/holding-subgraph.md)
   - [rimsky-compose multi-template project](agents/examples/rimsky-compose-multi-template.md)

   ## Errors

   - [Error catalog](agents/errors/README.md)

   ## Optional

   - [Glossary](glossary.md)
   - [Vocabulary discipline and deprecated terms](vocabulary.md)
   - [Dashboard UI guide](humans/dashboard.md)
   ```

   URLs are repository-relative paths (no `http://...`). They resolve correctly under `docs/agents/llms.txt` (since `concepts/` is `../concepts/` from there) — but the convention serves both contexts (the file at `docs/agents/llms.txt` and the build-copy at the repo root) by using paths relative to the docs/ root with the `agents/` prefix on agent-path links. **Concrete rule**: paths in `llms.txt` are relative to the docs root (so `concepts/four-layer-model.md`, not `../concepts/four-layer-model.md`). The llms.txt-validity lint resolves both from `docs/agents/` and from the `repo-root` argument; either resolution succeeding is sufficient.

**Verification:**

```sh
test -f docs/agents/llms.txt && echo OK
# llms.txt-validity lint requires the repo-root copies (Task 22) and llms-full.txt (Task 22).
# Defer full llms-txt-validity run until after Task 22.
```

---

## Task 22 — Generate `llms-full.txt` and create repo-root copies

**Files:** `docs/agents/llms-full.txt`, `llms.txt`, `llms-full.txt`.

**Steps:**

1. Run:

   ```sh
   make docs-llms-full
   make docs-roots
   ```

2. Verify:

   ```sh
   test -f docs/agents/llms-full.txt && test -f llms.txt && test -f llms-full.txt && echo OK
   diff docs/agents/llms.txt llms.txt
   diff docs/agents/llms-full.txt llms-full.txt
   ```

3. Run the full llms-txt-validity lint:

   ```sh
   go run ./cmd/rimsky-docs-lint llms-txt-validity
   ```

**Verification:**

```sh
test -f docs/agents/llms-full.txt && test -f llms.txt && test -f llms-full.txt && echo OK
diff -q docs/agents/llms.txt llms.txt
diff -q docs/agents/llms-full.txt llms-full.txt
go run ./cmd/rimsky-docs-lint llms-txt-validity
```

---

## Task 23 — Author `docs/humans/landing.md`

**Files:** `docs/humans/landing.md`.

**Steps:**

1. Create `docs/humans/landing.md` per spec §10.1. Three blocks:

   ```markdown
   # Rimsky

   ## What it is

   Rimsky is a project-agnostic reactive node-graph orchestration platform. Workflows are declarative graphs of nodes that communicate via two messages (`invalidate`, `recalculate`) and coordinate via claims and locks. The system is organized as four layers — see [`concepts/four-layer-model.md`](../concepts/four-layer-model.md).

   ## Point your favorite coding agent at this surface

   Rimsky's documentation is built for agent-mediated adoption. The recommended consumption path is through your coding agent. Useful entry points:

   - **For grounding**: [`concepts/four-layer-model.md`](../concepts/four-layer-model.md) — the meta-frame; everything else is layered on top.
   - **For agents that follow the convention**: [`agents/llms.txt`](../agents/llms.txt) — curated index pointing at every public-surface page.
   - **For implementing a custom claim producer, executor, or lifecycle subscriber**: [`protocols/`](../protocols/).
   - **For a narrative concept walk in learning order**: [`humans/concepts.md`](concepts.md).
   - **For copy-pasteable starter examples**: [`agents/examples/`](../agents/examples/).

   ## Dashboard

   The bundled `dashboards/` collection is a read-only reference UI for observing what Rimsky is doing. Launch it via the docker-compose stack (`docker compose -f deploy/docker-compose.yml --profile dashboard up`) or as a standalone container. See [`dashboard.md`](dashboard.md) for the screen-by-screen guide.
   ```

   No diagrams; no analogies; no positioning claims.

**Verification:**

```sh
test -f docs/humans/landing.md && echo OK
go run ./cmd/rimsky-docs-lint vocabulary
```

---

## Task 24 — Author `docs/humans/concepts.md`

**Files:** `docs/humans/concepts.md`.

**Steps:**

1. Create `docs/humans/concepts.md` per spec §10.2. Narrative walk in learning order. Each section names the concept once, links to its concept file, and walks the concept narratively. Definition-shaped sentences use the citation-drift convention.

   Section order:
   1. The four-layer model (the meta-frame)
   2. Nodes and node states (the unit of work)
   3. Templates and instances (the declarative artifacts)
   4. Frames and frame resolution (cascade resolution units)
   5. Cascades (`invalidate` propagation)
   6. Claims, claim handles, scopes, named locks (coordination)
   7. Write semantics (concurrency model)
   8. Holding subgraphs and inheritance (claim-lifetime extension)
   9. Service protocols (the three external-implementation surfaces)
   10. Attributes and userdata (substitution and writeback)

2. Each section pulls definition-shaped text from the relevant concept file via the citation-drift convention. Example fragment:

   ```markdown
   ## Frames and frame resolution

   <!-- @source: concepts/frame.md -->
   > A frame is the unit of cascade resolution. Every rimsky_worker_request row carries a non-null frame_id. At most one frame is `running` per instance.

   A frame begins when a node receives an `invalidate` (or is force-fired). It ends when no node remains in `stale` or `running` for the instance. Frame-end is computed at every scheduler tick.

   See [`concepts/frame.md`](../concepts/frame.md) for the consumer-visible guarantees.
   See [`concepts/frame-resolution.md`](../concepts/frame-resolution.md) for the resolution semantics.
   ```

3. After authoring, run citation-drift to confirm every cited blockquote matches the source frontmatter:

   ```sh
   go run ./cmd/rimsky-docs-lint citation-drift -scope=docs/humans -concepts-dir=docs/concepts
   ```

**Verification:**

```sh
test -f docs/humans/concepts.md && echo OK
go run ./cmd/rimsky-docs-lint citation-drift -scope=docs/humans -concepts-dir=docs/concepts
go run ./cmd/rimsky-docs-lint vocabulary
```

---

## Task 25 — Author `docs/humans/dashboard.md`

**Files:** `docs/humans/dashboard.md`.

**Steps:**

1. Lift content from `docs/specs/2026-05-02-dashboard-and-observability-design.md` and inspect the actual `dashboards/` implementation under the rimsky tree to identify the screens. Restructure for end-user usage rather than design rationale. Include sections:

   - **What it is.** Read-only UI for observing rimsky deployments. Composes the three observability protocols.
   - **Launching.** docker-compose profile (`--profile dashboard`), standalone container, k8s service. State the URL each renders at (e.g. `http://localhost:8090`).
   - **Screens.** One subsection per screen (instance list, node graph view, frame timeline, claim-handle inspector). For each: what it shows, how to read it, common diagnostic patterns.
   - **What it does NOT do.** No write actions in v1 (no force-fire, no invalidate, no register/deploy). Use the control-api or `rimsky-cli` for those.

2. Cite concept files where relevant via the citation-drift convention (e.g. when introducing "frame" use `<!-- @source: concepts/frame.md -->` + matching blockquote).

**Verification:**

```sh
test -f docs/humans/dashboard.md && echo OK
go run ./cmd/rimsky-docs-lint citation-drift -scope=docs/humans -concepts-dir=docs/concepts
go run ./cmd/rimsky-docs-lint vocabulary
```

---

## Task 26 — Update `CLAUDE.md` "Where to look first"

**Files:** `CLAUDE.md`.

**Steps:**

1. Open `CLAUDE.md` and locate the "Where to look first" section.

2. Add public-surface entries near the top of that section (above the existing internal references), preserving the existing entries below so working sessions can still reach them. Suggested addition:

   ```markdown
   ## Where to look first

   For external-consumer-facing material (cite from agents and external docs):

   - Public concepts reference: `docs/concepts/` (canonical per-noun reference)
   - Protocol-implementation guides: `docs/protocols/` (custom claim-producer/executor/lifecycle-subscriber)
   - Agent-shaped indices: `docs/agents/llms.txt`, `docs/agents/llms-full.txt`
   - Human-shaped narrative onboarding: `docs/humans/landing.md`, `docs/humans/concepts.md`, `docs/humans/dashboard.md`
   - Public glossary (auto-generated): `docs/glossary.md`
   - Public vocabulary discipline / deprecated terms: `docs/vocabulary.md`

   For internal/working engineering material (do NOT cite from public surfaces):

   - <existing entries: foundation contract, modeling contract, etc.>
   ```

   Keep the existing internal entries intact below the new public block.

**Verification:**

```sh
grep -q "docs/concepts/" CLAUDE.md && grep -q "docs/protocols/" CLAUDE.md && echo OK
```

---

## Task 27 — Wire `make docs-lint` into CI

**Files:** depends on the existing CI config; check for `.github/workflows/*.yml` or any other CI definition.

**Steps:**

1. Locate the existing CI configuration. If `.github/workflows/` exists, find the workflow that runs `make lint` / `make test`. If no CI config exists, create `.github/workflows/docs-lint.yml`:

   ```yaml
   name: docs-lint
   on:
     pull_request:
     push:
       branches: [main]
   jobs:
     docs-lint:
       runs-on: ubuntu-latest
       steps:
         - uses: actions/checkout@v4
         - uses: actions/setup-go@v5
           with:
             go-version: '1.25.0'
         - name: Verify documentation surface
           run: make docs-lint
   ```

2. If a workflow already exists for Go checks, add `make docs-lint` as an additional step there rather than creating a new workflow file. Pattern-match on the existing step structure.

3. Confirm the lint runs cleanly locally before adding to CI:

   ```sh
   make docs-lint
   ```

**Verification:**

```sh
make docs-lint
# Find at least one workflow file referencing docs-lint:
grep -r "docs-lint" .github/ 2>/dev/null && echo OK
```

---

## Task 28 — Final sweep: end-to-end docs build

**Files:** none new; verifies the cumulative state.

**Steps:**

1. Regenerate everything from sources to confirm the build is reproducible:

   ```sh
   make docs-build
   ```

   `docs-build` runs `docs-glossary`, `docs-llms-full`, and `docs-roots` (per Task 11).

2. Run the full lint suite:

   ```sh
   make docs-lint
   ```

3. Confirm the working tree has no uncommitted changes from the regeneration (i.e. committed `docs/glossary.md`, `docs/agents/llms-full.txt`, `llms.txt`, `llms-full.txt` already match generator output):

   ```sh
   git status --porcelain docs/glossary.md docs/agents/llms-full.txt llms.txt llms-full.txt
   ```

   Output must be empty.

4. Run the existing repo-wide checks to confirm nothing else broke:

   ```sh
   go build ./...
   go test ./...
   make lint
   make license-lint
   ```

**Verification:**

```sh
make docs-build
make docs-lint
go build ./...
go test ./...
make lint
make license-lint
test -z "$(git status --porcelain docs/glossary.md docs/agents/llms-full.txt llms.txt llms-full.txt)"
```

All exit 0; the `git status` check produces no output.

---

## Manual checks after completion

None. Every check in this plan is automated. The lint suite, the generator parity checks, and the test suite collectively cover correctness of the public surface.

If a future change introduces a manual-verification step (e.g. eyeballing a rendered SVG once an SSG lands), that goes into the spec for that future change, not here.
