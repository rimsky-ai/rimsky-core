// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

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
	ProtoSymbol     string       `yaml:"proto_symbol"`
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
