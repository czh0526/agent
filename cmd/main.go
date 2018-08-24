package main

import (
	"fmt"
	"os"
	"time"

	"github.com/czh0526/agent/agent"

	cli "gopkg.in/urfave/cli.v1"
)

var (
	app         = cli.NewApp()
	globalAgent Agent
)

func init() {
	app.Action = startAgent
	app.After = func(ctx *cli.Context) error {
		if globalAgent != nil {
			if err := globalAgent.Stop(); err != nil {
				return err
			}
		}
		time.Sleep(time.Second * 2)
		return nil
	}
}

func main() {
	if err := app.Run(os.Args); err != nil {
		fmt.Println("app.Run() error: %v", err)
		os.Exit(1)
	}
}

func startAgent(ctx *cli.Context) error {
	var err error

	fmt.Println("main.Action begin.")
	// 构建 Agent
	if globalAgent, err = makeAgent(ctx); err != nil {
		return err
	}

	// 启动 Agent
	if err = globalAgent.Start(); err != nil {
		return err
	}

	// 持续运行 10 秒
	globalAgent.Wait()
	fmt.Println("main.Action end.")

	return nil
}

func makeAgent(ctx *cli.Context) (Agent, error) {
	return agent.NewAgent()
}