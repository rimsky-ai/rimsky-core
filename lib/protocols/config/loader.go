// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// @decision: config-yaml-loading-policy
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

var bracedEnvRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func ExpandEnv(path, raw string) (string, error) {
	missing := map[string]struct{}{}
	expanded := bracedEnvRefPattern.ReplaceAllStringFunc(raw, func(ref string) string {
		name := ref[2 : len(ref)-1]
		v, ok := os.LookupEnv(name)
		if !ok {
			missing[name] = struct{}{}
			return ""
		}
		return v
	})
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for n := range missing {
			names = append(names, n)
		}
		sort.Strings(names)
		return "", fmt.Errorf("config %q: unset environment variable(s) referenced by ${...}: %v", path, names)
	}
	return expanded, nil
}

// @decision: config-format-yaml
func DecodeStrict(path string, data []byte, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("parse config %q: %w", path, err)
	}
	return nil
}

func LoadFile(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %q: %w", path, err)
	}
	expanded, err := ExpandEnv(path, string(raw))
	if err != nil {
		return err
	}
	return DecodeStrict(path, []byte(expanded), out)
}
