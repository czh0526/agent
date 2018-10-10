package utils

import cli "gopkg.in/urfave/cli.v1"

var (
	DataDirFlag = DirectoryFlag{
		Name:  "datadir",
		Usage: "Data directory for the database and keystore",
		Value: DirectoryString{"e:\\mydata\\agent\\"},
	}

	JSpathFlag = cli.StringFlag{
		Name:  "jspath",
		Usage: "JavaSript root path for `loadScript`",
		Value: ".",
	}

	ExecFlag = cli.StringFlag{
		Name:  "exec",
		Usage: "Execute JavaScript statement",
	}

	PreloadJSFlag = cli.StringFlag{
		Name:  "preload",
		Usage: "Execute JavaScript statement",
	}
)
