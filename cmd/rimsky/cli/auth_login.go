// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: rimsky
// @concept: api-key
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"golang.org/x/term"
)

func RunAuthLogin(ctx context.Context, args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "usage: rimsky auth login (interactive; reads URL and api-key from the terminal)")
		return 2
	}

	cfgPath, err := DefaultConfigPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	ctxName := cfg.CurrentContext
	if envCtx := os.Getenv("RIMSKY_CONTEXT"); envCtx != "" {
		ctxName = envCtx
	}
	if ctxName == "" {
		ctxName = "default"
	}
	current := cfg.Contexts[ctxName]

	reader := bufio.NewReader(os.Stdin)

	url, err := promptURL(reader, current.Endpoint)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if url == "" {
		fmt.Fprintln(os.Stderr, "rimsky auth login: a control-api URL is required")
		return 2
	}

	key, err := promptAPIKey(reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if key == "" {
		fmt.Fprintln(os.Stderr, "rimsky auth login: an api-key is required")
		return 2
	}

	if _, statusErr := fetchAuthStatus(ctx, url, key); statusErr != nil {
		var apiErr *APIError
		if errors.As(statusErr, &apiErr) && apiErr.Status == http.StatusUnauthorized {
			fmt.Fprintln(os.Stderr, "rimsky auth login: the api-key was rejected (401)")
			return 1
		}
		fmt.Fprintf(os.Stderr, "rimsky auth login: could not reach %s: %v\n", url, statusErr)
		return 1
	}

	if cfg.Contexts == nil {
		cfg.Contexts = map[string]Context{}
	}
	current.Endpoint = url
	current.APIKey = key
	cfg.Contexts[ctxName] = current
	if cfg.CurrentContext == "" {
		cfg.CurrentContext = ctxName
	}
	if err := SaveConfig(cfgPath, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "logged in to %s (context %q)\n", url, ctxName)
	return 0
}

func promptURL(reader *bufio.Reader, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(os.Stdout, "Control-API URL [%s]: ", def)
	} else {
		fmt.Fprint(os.Stdout, "Control-API URL: ")
	}
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}

func promptAPIKey(reader *bufio.Reader) (string, error) {
	fmt.Fprint(os.Stdout, "API key: ")
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		raw, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stdout)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(raw)), nil
	}
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
