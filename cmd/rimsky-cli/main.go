// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// main.go — rimsky-cli entry point. Dispatches subcommands to handlers
// in control/cli/. Hand-rolled subcommand routing on os.Args[1].
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/fallguy/rimsky/control/cli"
	"github.com/fallguy/rimsky/control/cli/compose"
)

func main() {
	if len(os.Args) < 2 {
		printRootUsage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Printf("rimsky-cli %s\n", version)
		return
	case "help", "--help", "-h":
		printRootUsage(os.Stdout)
		return
	case "health":
		os.Exit(cli.RunHealth(context.Background(), os.Args[2:]))
	case "template":
		os.Exit(dispatchTemplate(os.Args[2:]))
	case "tag":
		os.Exit(dispatchTag(os.Args[2:]))
	case "instance":
		os.Exit(dispatchInstance(os.Args[2:]))
	case "node":
		os.Exit(dispatchNode(os.Args[2:]))
	case "admin":
		os.Exit(dispatchAdmin(os.Args[2:]))
	case "parked":
		os.Exit(dispatchParked(os.Args[2:]))
	case "ctx":
		os.Exit(dispatchCtx(os.Args[2:]))
	case "run":
		os.Exit(cli.RunRun(context.Background(), os.Args[2:]))
	case "register":
		os.Exit(cli.RunRegister(context.Background(), os.Args[2:]))
	case "deploy":
		os.Exit(cli.RunDeploy(context.Background(), os.Args[2:]))
	case "undeploy":
		os.Exit(cli.RunUndeploy(context.Background(), os.Args[2:]))
	case "instantiate":
		os.Exit(cli.RunInstantiate(context.Background(), os.Args[2:]))
	case "rm-instance":
		os.Exit(cli.RunRmInstance(context.Background(), os.Args[2:]))
	case "ls":
		os.Exit(cli.RunLs(context.Background(), os.Args[2:]))
	case "logs":
		os.Exit(cli.RunLogs(context.Background(), os.Args[2:]))
	case "init":
		os.Exit(cli.RunInit(context.Background(), os.Args[2:]))
	case "compose":
		os.Exit(compose.Dispatch(context.Background(), os.Args[2:]))
	case "dev":
		os.Exit(compose.DispatchDev(context.Background(), os.Args[2:]))
	default:
		fmt.Fprintf(os.Stderr, "rimsky-cli: unknown command %q\n\n", os.Args[1])
		printRootUsage(os.Stderr)
		os.Exit(2)
	}
}

func dispatchTemplate(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli template <register|list|get|deploy|undeploy|rm> ...")
		return 2
	}
	ctx := context.Background()
	rest := args[1:]
	switch args[0] {
	case "register":
		return cli.RunTemplateRegister(ctx, rest)
	case "list":
		return cli.RunTemplateList(ctx, rest)
	case "get":
		return cli.RunTemplateGet(ctx, rest)
	case "deploy":
		return cli.RunTemplateDeploy(ctx, rest)
	case "undeploy":
		return cli.RunTemplateUndeploy(ctx, rest)
	case "rm":
		return cli.RunTemplateRm(ctx, rest)
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky-cli template <register|list|get|deploy|undeploy|rm> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky-cli template: unknown subcommand %q\n", args[0])
	return 2
}

func dispatchTag(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli tag <create|list|get|mv|rm> ...")
		return 2
	}
	ctx := context.Background()
	rest := args[1:]
	switch args[0] {
	case "create":
		return cli.RunTagCreate(ctx, rest)
	case "list":
		return cli.RunTagList(ctx, rest)
	case "get":
		return cli.RunTagGet(ctx, rest)
	case "mv":
		return cli.RunTagMv(ctx, rest)
	case "rm":
		return cli.RunTagRm(ctx, rest)
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky-cli tag <create|list|get|mv|rm> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky-cli tag: unknown subcommand %q\n", args[0])
	return 2
}

func dispatchInstance(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli instance <create|list|get|delete|nodes|events> ...")
		return 2
	}
	ctx := context.Background()
	rest := args[1:]
	switch args[0] {
	case "create":
		return cli.RunInstanceCreate(ctx, rest)
	case "list":
		return cli.RunInstanceList(ctx, rest)
	case "get":
		return cli.RunInstanceGet(ctx, rest)
	case "delete":
		return cli.RunInstanceDelete(ctx, rest)
	case "nodes":
		return cli.RunInstanceNodes(ctx, rest)
	case "events":
		return cli.RunInstanceEvents(ctx, rest)
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky-cli instance <create|list|get|delete|nodes|events> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky-cli instance: unknown subcommand %q\n", args[0])
	return 2
}

func dispatchNode(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli node <get> ...")
		return 2
	}
	ctx := context.Background()
	rest := args[1:]
	switch args[0] {
	case "get":
		return cli.RunNodeGet(ctx, rest)
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky-cli node <get> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky-cli node: unknown subcommand %q\n", args[0])
	return 2
}

func dispatchParked(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli parked <list> ...")
		return 2
	}
	ctx := context.Background()
	rest := args[1:]
	switch args[0] {
	case "list":
		return cli.RunParkedList(ctx, rest)
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky-cli parked <list> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky-cli parked: unknown subcommand %q\n", args[0])
	return 2
}

func dispatchAdmin(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli admin <force-fire|invalidate|reset> ...")
		return 2
	}
	ctx := context.Background()
	rest := args[1:]
	switch args[0] {
	case "force-fire":
		return cli.RunAdminForceFire(ctx, rest)
	case "invalidate":
		return cli.RunAdminInvalidate(ctx, rest)
	case "reset":
		return cli.RunAdminReset(ctx, rest)
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky-cli admin <force-fire|invalidate|reset> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky-cli admin: unknown subcommand %q\n", args[0])
	return 2
}

func dispatchCtx(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky-cli ctx <list|use|add|rm|current> ...")
		return 2
	}
	rest := args[1:]
	cfgPath, err := cli.DefaultConfigPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	switch args[0] {
	case "list":
		return cli.RunCtxList(rest, cfgPath)
	case "use":
		return cli.RunCtxUse(rest, cfgPath)
	case "add":
		return cli.RunCtxAdd(rest, cfgPath)
	case "rm":
		return cli.RunCtxRm(rest, cfgPath)
	case "current":
		return cli.RunCtxCurrent(rest, cfgPath)
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky-cli ctx <list|use|add|rm|current> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky-cli ctx: unknown subcommand %q\n", args[0])
	return 2
}

func printRootUsage(w io.Writer) {
	fmt.Fprintln(w, "rimsky-cli — orchestration CLI for the rimsky platform.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Dev-loop:")
	fmt.Fprintln(w, "  run <file>            Register, deploy, instantiate in one shot")
	fmt.Fprintln(w, "  register <file>")
	fmt.Fprintln(w, "  deploy <ref>")
	fmt.Fprintln(w, "  undeploy <ref>")
	fmt.Fprintln(w, "  instantiate <ref>")
	fmt.Fprintln(w, "  rm-instance <id>      Delete a terminal instance")
	fmt.Fprintln(w, "  ls [templates|instances|tags]")
	fmt.Fprintln(w, "  logs <id-or-key>      Stream events (poll-based)")
	fmt.Fprintln(w, "  health")
	fmt.Fprintln(w, "  init [<dir>]          Scaffold a starter project")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Compose:")
	fmt.Fprintln(w, "  compose up | down | plan | status")
	fmt.Fprintln(w, "  dev up | down | status")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Literal API:")
	fmt.Fprintln(w, "  template register | list | get | deploy | undeploy | rm")
	fmt.Fprintln(w, "  tag create | list | get | mv | rm")
	fmt.Fprintln(w, "  instance create | list | get | delete | nodes | events")
	fmt.Fprintln(w, "  node get")
	fmt.Fprintln(w, "  admin force-fire | invalidate | reset")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Context:")
	fmt.Fprintln(w, "  ctx list | use | add | rm | current")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Common flags (all verbs):")
	fmt.Fprintln(w, "  --endpoint <url>     Override control-api endpoint")
	fmt.Fprintln(w, "  -o human|json        Output format (default human)")
	fmt.Fprintln(w, "  --no-color           Disable ANSI color")
	fmt.Fprintln(w, "  --yes                Confirm destructive operations")
	fmt.Fprintln(w, "  -h, --help           Show this help")
}
