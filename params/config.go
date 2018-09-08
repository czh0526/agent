package params

var (
	GlobalConfig Config
)

type Config struct {
	LogParams LogParams
}

type LogParams struct {
	Verbosity int
	Vmodule   string
}
