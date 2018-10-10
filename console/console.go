package console

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/czh0526/agent/internal/jsre"
	"github.com/czh0526/agent/rpc"
	"github.com/robertkrimen/otto"
)

const HistoryFile = "history"

type Console struct {
	client   *rpc.Client
	jsre     *jsre.JSRE
	prompt   string
	prompter UserPrompter
	histPath string
	history  []string
	printer  io.Writer
}

func New(client *rpc.Client, printer io.Writer, datadir string, preload []string) (*Console, error) {
	console := &Console{
		client:   client,
		jsre:     jsre.New(datadir, printer),
		printer:  printer,
		histPath: filepath.Join(datadir, HistoryFile),
	}

	if err := os.MkdirAll(datadir, 0700); err != nil {
		return nil, err
	}
	if err := console.init(preload); err != nil {
		return nil, err
	}

	return console, nil
}

func (c *Console) init(preload []string) error {

	bridge := newBridge(c.client, c.prompter, c.printer)
	c.jsre.Set("jagent", struct{}{})

	jagentObj, _ := c.jsre.Get("jagent")
	jagentObj.Object().Set("send", bridge.Send)
	jagentObj.Object().Set("sendAsync", bridge.Send)

	// 关联 JavaScript 的 Console.log/error 函数
	consoleObj, _ := c.jsre.Get("console")
	consoleObj.Object().Set("log", c.consoleOutput)
	consoleObj.Object().Set("error", c.consoleOutput)

	if err := c.jsre.Compile("bignumber.js", jsre.BigNumber_JS); err != nil {
		return fmt.Errorf("bignumber.js: %v", err)
	}
	if err := c.jsre.Compile("web3.js", jsre.Web3_JS); err != nil {
		return fmt.Errorf("web3.js: %v", err)
	}

	if _, err := c.jsre.Run("var Web3 = require('web3');"); err != nil {
		return fmt.Errorf("web3 require: %v", err)
	}
	if _, err := c.jsre.Run("var web3 = new Web3(jagent);"); err != nil {
		return fmt.Errorf("web3 provider: %v", err)
	}

	if web3, err := c.jsre.Get("web3"); err != nil {
		return fmt.Errorf("Get web3 error: %v", err)
	} else {
		fmt.Println(web3)
	}

	for _, path := range preload {
		if err := c.jsre.Exec(path); err != nil {
			failure := err.Error()
			if ottoErr, ok := err.(*otto.Error); ok {
				failure = ottoErr.String()
			}
			return fmt.Errorf("%s: %v", path, failure)
		}
	}

	return nil
}

func (c *Console) Stop(graceful bool) error {
	if err := ioutil.WriteFile(c.histPath, []byte(strings.Join(c.history, "\n")), 0600); err != nil {
		return err
	}
	if err := os.Chmod(c.histPath, 0600); err != nil {
		return err
	}

	c.client.Close()
	c.jsre.Stop(graceful)
	return nil
}

func (c *Console) consoleOutput(call otto.FunctionCall) otto.Value {
	output := []string{}
	for _, argument := range call.ArgumentList {
		output = append(output, fmt.Sprintf("%v", argument))
	}
	fmt.Fprintln(c.printer, strings.Join(output, " "))
	return otto.Value{}
}

func (c *Console) Execute(path string) error {
	return c.jsre.Exec(path)
}

func (c *Console) Evaluate(statement string) error {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(c.printer, "[native] error: %v \n", r)
		}
	}()
	return c.jsre.Evaluate(statement, c.printer)
}

func (c *Console) Welcome() {
	fmt.Fprintf(c.printer, "Welcome to the agent JavaScript console!\n\n")

	c.jsre.Evaluate("web3.version", c.printer)
	c.jsre.Run(`
		console.log("Hello JSRE.")
		//console.log("instance: " + web3.version.node);
		//console.log("coinbase: " + eth.coinbase);
		//console.log(" datadir: " + admin.datadir); 
	`)
}
