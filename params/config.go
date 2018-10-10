package params

import homedir "github.com/mitchellh/go-homedir"

var (
	HomeDir      string
	GlobalConfig Config
)

func init() {
	var err error
	HomeDir, err = homedir.Expand("~/proton")
	if err != nil {
		panic(err)
	}
}

type Config struct {
	LogParams LogParams
}

type LogParams struct {
	Verbosity int
	Vmodule   string
}
