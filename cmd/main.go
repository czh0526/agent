package main

import (
	"fmt"
	"os"
	"time"

	"github.com/czh0526/agent/log"

	"github.com/czh0526/agent/agent"

	cli "gopkg.in/urfave/cli.v1"
)

var (
	app         = cli.NewApp()
	globalAgent Agent
)

func init() {

	app.Flags = append(app.Flags, cli.IntFlag{
		Name:  "verbosity",
		Usage: "Logging verbosity: 0=silent, 1=error, 2=warn, 3=info, 4=debug, 5=detail",
		Value: 3,
	})
	app.Flags = append(app.Flags, cli.StringFlag{
		Name:  "vmodule",
		Usage: "Per-module verbosity: comma-separated list of <pattern>=<level> (e.g. eth/*=5,p2p=4)",
		Value: "",
	})

	app.Action = startAgent
	app.Before = func(ctx *cli.Context) error {
		// 构造日志文件
		fileHandler, err := log.FileHandler("./agent.log", log.LogfmtFormat())
		if err != nil {
			fmt.Printf("init log file error: %v", err)
			os.Exit(-1)
		}
		// 初始化日志系统
		glogger := log.NewGlogHandler(fileHandler)
		glogger.Verbosity(log.Lvl(ctx.GlobalInt("verbosity")))
		glogger.Vmodule(ctx.GlobalString("vmodule"))
		log.Root().SetHandler(glogger)
		log.Info("日志系统初始化成功.")

		return nil
	}
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
		log.Error("app.Run() error: %v", err)
		os.Exit(1)
	}
}

func startAgent(ctx *cli.Context) error {
	var err error

	log.Info("main.Action 启动.")
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
	log.Info("main.Action end.")

	return nil
}

func makeAgent(ctx *cli.Context) (Agent, error) {
	return agent.NewAgent()
}
