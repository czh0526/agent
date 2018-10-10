package main

import (
	"fmt"
	"os"

	"github.com/czh0526/agent/cmd/utils"
	"github.com/czh0526/agent/console"
	"github.com/czh0526/agent/rpc"
	cli "gopkg.in/urfave/cli.v1"
)

var (
	consoleFlags = []cli.Flag{utils.JSpathFlag, utils.ExecFlag, utils.PreloadJSFlag}

	attachCommand = cli.Command{
		Action:    remoteConsole,
		Name:      "attach",
		Usage:     "Start an interactive JavaScript environment (connect to node)",
		ArgsUsage: "[endpoint]",
		Flags:     append(consoleFlags, utils.DataDirFlag),
	}

	consoleCommand = cli.Command{
		Action: localConsole,
		Name:   "console",
		Usage:  "Start an interactive JavaScript environment",
		Flags:  append(append(nodeFlags, rpcFlags...), consoleFlags...),
	}
)

func remoteConsole(ctx *cli.Context) error {
	endpoint := ctx.Args().First()
	if endpoint == "" {
		endpoint = `\\.\pipe\agent.ipc`
	}
	client, err := rpc.Dial(endpoint)
	if err != nil {
		panic(fmt.Sprintf("Unable to attach to remote agent: %v", err))
	}

	console, err := console.New(client, os.Stdout, "testdata", []string{})
	if err != nil {
		panic(fmt.Sprintf("Failed to start the JavaScript console: %v", err))
	}
	defer console.Stop(false)

	return nil
}

func localConsole(ctx *cli.Context) error {
	fmt.Println("localConsole() has not be implemented.")
	return nil
}
