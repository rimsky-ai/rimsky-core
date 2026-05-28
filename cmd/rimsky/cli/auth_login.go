// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// auth_login.go — `rimsky auth login`. The convenience verb for a
// dev-machine user logging into an already-bootstrapped rimsky deployment:
// prompts for the control-api URL (defaulting to the active context's
// endpoint) and an api-key (read password-style off the terminal),
// optionally verifies the key against GET /auth/status, then writes the
// key into the active context's `api_key` field and saves the config. The
// host-agent reads this key for its outbound authentication to the
// host-agent-proxy.
//
// Sibling to `auth init` (does not replace it): `init` bootstraps an
// anonymous-mode deployment by minting the first admin key; `login` records
// an already-minted key locally for the dev-machine user.
//
// @concept: rimsky (CLI auth login verb)
// @concept: api-key
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"golang.org/x/term"
)

// RunAuthLogin implements `rimsky auth login`. Returns 0 on success, 1 on
// error, 2 on usage/flag error. The ctx is threaded from RunAuth (matching
// the sibling sub-handler convention).
func RunAuthLogin(ctx context.Context, args []string) int {
	// auth login takes no positional args; reject any so a stray token isn't
	// silently swallowed (it could be a key the user meant to type at the
	// prompt, and we never want a key on the command line / shell history).
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

	// Resolve the active context name. login writes into the current
	// context; if none is set we create a `default` one.
	ctxName := cfg.CurrentContext
	if envCtx := os.Getenv("RIMSKY_CONTEXT"); envCtx != "" {
		ctxName = envCtx
	}
	if ctxName == "" {
		ctxName = "default"
	}
	current := cfg.Contexts[ctxName] // zero Context if absent

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

	// Optional verification: hit GET /auth/status with the key. A transport
	// failure is surfaced as an error; a 401 means the key is rejected.
	if _, reachable := fetchAuthStatus(ctx, url, key); !reachable {
		// Distinguish "key rejected" from "endpoint unreachable" with a
		// follow-up call so the operator gets an actionable message.
		c := newAuthClient(url, key)
		var resp authStatusResp
		if _, callErr := c.RawCall(ctx, http.MethodGet, "/auth/status", nil, &resp); callErr != nil {
			var apiErr *APIError
			if errors.As(callErr, &apiErr) && apiErr.Status == http.StatusUnauthorized {
				fmt.Fprintln(os.Stderr, "rimsky auth login: the api-key was rejected (401)")
				return 1
			}
			fmt.Fprintf(os.Stderr, "rimsky auth login: could not reach %s: %v\n", url, callErr)
			return 1
		}
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

// promptURL reads the control-api URL from stdin, returning def when the
// user presses enter without typing anything.
func promptURL(reader *bufio.Reader, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(os.Stdout, "Control-API URL [%s]: ", def)
	} else {
		fmt.Fprint(os.Stdout, "Control-API URL: ")
	}
	line, err := reader.ReadString('\n')
	if err != nil && err.Error() != "EOF" {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}

// promptAPIKey reads the api-key off the terminal without echoing it. When
// stdin is not a terminal (e.g. piped input in tests / scripts) it falls
// back to a plain line read off the shared reader so the verb remains
// scriptable and so buffered bytes from the prior URL prompt aren't lost.
func promptAPIKey(reader *bufio.Reader) (string, error) {
	fmt.Fprint(os.Stdout, "API key: ")
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		raw, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stdout) // newline the suppressed Enter didn't echo
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(raw)), nil
	}
	line, err := reader.ReadString('\n')
	if err != nil && err.Error() != "EOF" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
