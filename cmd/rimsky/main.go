// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// main.go — rimsky entry point. Dispatches subcommands to handlers
// in cmd/rimsky/cli/. Hand-rolled subcommand routing on os.Args[1].
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

func main() {
	if len(os.Args) < 2 {
		printRootUsage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Printf("rimsky %s\n", version)
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
	case "agent":
		os.Exit(cli.RunAgent(os.Args[2:]))
	case "parked":
		os.Exit(dispatchParked(os.Args[2:]))
	case "messages":
		os.Exit(dispatchMessages(os.Args[2:]))
	case "backfill":
		os.Exit(dispatchBackfill(os.Args[2:]))
	case "asset":
		os.Exit(dispatchAsset(os.Args[2:]))
	case "lineage":
		os.Exit(dispatchLineage(os.Args[2:]))
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
	case "auth":
		os.Exit(cli.RunAuth(os.Args[2:]))
	case "conformance":
		os.Exit(dispatchConformance(os.Args[2:]))
	default:
		fmt.Fprintf(os.Stderr, "rimsky: unknown command %q\n\n", os.Args[1])
		printRootUsage(os.Stderr)
		os.Exit(2)
	}
}

func dispatchTemplate(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky template <register|list|get|deploy|undeploy|rm> ...")
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
		fmt.Fprintln(os.Stdout, "usage: rimsky template <register|list|get|deploy|undeploy|rm> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky template: unknown subcommand %q\n", args[0])
	return 2
}

func dispatchTag(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky tag <create|list|get|mv|rm> ...")
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
		fmt.Fprintln(os.Stdout, "usage: rimsky tag <create|list|get|mv|rm> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky tag: unknown subcommand %q\n", args[0])
	return 2
}

func dispatchInstance(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky instance <create|list|get|delete|nodes|events> ...")
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
		fmt.Fprintln(os.Stdout, "usage: rimsky instance <create|list|get|delete|nodes|events> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky instance: unknown subcommand %q\n", args[0])
	return 2
}

func dispatchNode(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky node <get> ...")
		return 2
	}
	ctx := context.Background()
	rest := args[1:]
	switch args[0] {
	case "get":
		return cli.RunNodeGet(ctx, rest)
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky node <get> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky node: unknown subcommand %q\n", args[0])
	return 2
}

func dispatchParked(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky parked <list> ...")
		return 2
	}
	ctx := context.Background()
	rest := args[1:]
	switch args[0] {
	case "list":
		return cli.RunParkedList(ctx, rest)
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky parked <list> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky parked: unknown subcommand %q\n", args[0])
	return 2
}

func dispatchAdmin(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky admin <invalidate|reset> ...")
		return 2
	}
	ctx := context.Background()
	rest := args[1:]
	switch args[0] {
	case "invalidate":
		return cli.RunAdminInvalidate(ctx, rest)
	case "reset":
		return cli.RunAdminReset(ctx, rest)
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky admin <invalidate|reset> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky admin: unknown subcommand %q\n", args[0])
	return 2
}

func dispatchMessages(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky messages <tail|show> ...")
		return 2
	}
	ctx := context.Background()
	rest := args[1:]
	switch args[0] {
	case "tail":
		return cli.RunMessagesTail(ctx, rest)
	case "show":
		return cli.RunMessagesShow(ctx, rest)
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky messages <tail|show> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky messages: unknown subcommand %q\n", args[0])
	return 2
}

func dispatchBackfill(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky backfill <create|list|show|cancel> ...")
		return 2
	}
	ctx := context.Background()
	rest := args[1:]
	switch args[0] {
	case "create":
		return cli.RunBackfillCreate(ctx, rest)
	case "list":
		return cli.RunBackfillList(ctx, rest)
	case "show":
		return cli.RunBackfillShow(ctx, rest)
	case "cancel":
		return cli.RunBackfillCancel(ctx, rest)
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky backfill <create|list|show|cancel> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky backfill: unknown subcommand %q\n", args[0])
	return 2
}

func dispatchAsset(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky asset <list|show|materialize|versions|delete|lineage> ...")
		return 2
	}
	ctx := context.Background()
	rest := args[1:]
	switch args[0] {
	case "list":
		return cli.RunAssetList(ctx, rest)
	case "show":
		return cli.RunAssetShow(ctx, rest)
	case "materialize":
		return cli.RunAssetMaterialize(ctx, rest)
	case "versions":
		return cli.RunAssetVersions(ctx, rest)
	case "delete":
		return cli.RunAssetDelete(ctx, rest)
	case "lineage":
		return cli.RunAssetLineage(ctx, rest)
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky asset <list|show|materialize|versions|delete|lineage> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky asset: unknown subcommand %q\n", args[0])
	return 2
}

func dispatchLineage(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky lineage <prune> ...")
		return 2
	}
	ctx := context.Background()
	rest := args[1:]
	switch args[0] {
	case "prune":
		return cli.RunLineagePrune(ctx, rest)
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, "usage: rimsky lineage <prune> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky lineage: unknown subcommand %q\n", args[0])
	return 2
}

func dispatchCtx(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky ctx <list|use|add|rm|current> ...")
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
		fmt.Fprintln(os.Stdout, "usage: rimsky ctx <list|use|add|rm|current> ...")
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky ctx: unknown subcommand %q\n", args[0])
	return 2
}

func printRootUsage(w io.Writer) {
	fmt.Fprintln(w, "rimsky — orchestration CLI for the rimsky platform.")
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
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Literal API:")
	fmt.Fprintln(w, "  template register | list | get | deploy | undeploy | rm")
	fmt.Fprintln(w, "  tag create | list | get | mv | rm")
	fmt.Fprintln(w, "  instance create | list | get | delete | nodes | events")
	fmt.Fprintln(w, "  node get")
	fmt.Fprintln(w, "  admin invalidate | reset")
	fmt.Fprintln(w, "  messages tail | show")
	fmt.Fprintln(w, "  backfill create | list | show | cancel")
	fmt.Fprintln(w, "  asset list | show | materialize | versions | delete | lineage")
	fmt.Fprintln(w, "  lineage prune")
	fmt.Fprintln(w, "  parked list")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Auth:")
	fmt.Fprintln(w, "  auth init                    Mint admin key in anonymous mode")
	fmt.Fprintln(w, "  auth create-key --name=... --role=...")
	fmt.Fprintln(w, "  auth list | show | revoke | rotate | status")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Context:")
	fmt.Fprintln(w, "  ctx list | use | add | rm | current")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Host agent:")
	fmt.Fprintln(w, "  agent start | status | stop      Manage the local host-agent daemon")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Conformance:")
	fmt.Fprintln(w, "  conformance executor | claim-producer | publisher | validation |")
	fmt.Fprintln(w, "              data-processing | blob-backend | probe")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Common flags (all verbs):")
	fmt.Fprintln(w, "  --endpoint <url>     Override control-api endpoint")
	fmt.Fprintln(w, "  -o human|json        Output format (default human)")
	fmt.Fprintln(w, "  --no-color           Disable ANSI color")
	fmt.Fprintln(w, "  --yes                Confirm destructive operations")
	fmt.Fprintln(w, "  -h, --help           Show this help")
}
