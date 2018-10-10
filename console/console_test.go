package console

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"github.com/czh0526/agent/agent"
	"github.com/czh0526/agent/rpc"
)

type tester struct {
	workspace string
	stack     *agent.Agent
	console   *Console
	output    *bytes.Buffer
}

func newTester(t *testing.T, confOverride func()) *tester {

	// 构建一个临时目录
	workspace, err := ioutil.TempDir("", "console-tester-")
	if err != nil {
		t.Fatalf("failed to create temporary keystore: %v", err)
	}

	// 创建 agent.Agent, 启动 RPC 服务
	agent, err := agent.NewAgent()
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}
	if err = agent.Start(); err != nil {
		t.Fatalf("failed to start agent: %v", err)
	}

	// 创建 rpc.Client, 接入 RPC 服务
	//client := rpc.DialInProc(agent.RPCServer.InprocHandler())
	ctx := context.Background()
	client, err := rpc.DialWebsocket(ctx, "ws://127.0.0.1:8545", "")
	printer := new(bytes.Buffer)

	// 创建 Console, Console ==> rpc.Client ==> agent.Agent
	console, err := New(client, printer, "testdata", []string{"preload.js"})
	if err != nil {
		t.Fatalf("failed to new console: %v", err)
	}

	return &tester{
		workspace: workspace,
		stack:     agent,
		console:   console,
		output:    printer,
	}
}

func (env *tester) Close(t *testing.T) {
	if err := env.console.Stop(false); err != nil {
		t.Errorf("failed to stop embedded console: %v", err)
	}
	if err := env.stack.Stop(); err != nil {
		t.Errorf("failed to stop embeded agent: %v", err)
	}
	os.RemoveAll(env.workspace)
}

func TestPreload(t *testing.T) {
	tester := newTester(t, nil)
	defer tester.Close(t)

	tester.console.Evaluate("preloaded")

	output := tester.output.String()
	fmt.Printf(">> %v\n", output)
}

func TestExecute(t *testing.T) {
	tester := newTester(t, nil)
	defer tester.Close(t)

	tester.console.Execute("exec.js")
	tester.console.Evaluate("execed")

	output := tester.output.String()
	fmt.Printf(">> %v\n", output)
}

func TestWelcome(t *testing.T) {
	tester := newTester(t, nil)
	defer tester.Close(t)

	tester.console.Welcome()

	output := tester.output.String()
	fmt.Printf(">> %v", output)
}
