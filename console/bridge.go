package console

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/czh0526/agent/log"
	"github.com/czh0526/agent/rpc"
	"github.com/robertkrimen/otto"
)

type bridge struct {
	client   *rpc.Client
	prompter UserPrompter
	printer  io.Writer
}

type jsonrpcCall struct {
	ID     int64
	Method string
	Params []interface{}
}

func newBridge(client *rpc.Client, prompter UserPrompter, printer io.Writer) *bridge {
	return &bridge{
		client:   client,
		prompter: prompter,
		printer:  printer,
	}
}

func (b *bridge) Send(call otto.FunctionCall) (response otto.Value) {
	JSON, _ := call.Otto.Object("JSON")
	reqVal, err := JSON.Call("stringify", call.Argument(0))
	if err != nil {
		throwJSException(err.Error())
	}

	var (
		rawReq = reqVal.String()
		dec    = json.NewDecoder(strings.NewReader(rawReq))
		reqs   []jsonrpcCall
		batch  bool
	)
	dec.UseNumber()
	if rawReq[0] == '[' {
		batch = true
		dec.Decode(&reqs)
	} else {
		batch = false
		reqs = make([]jsonrpcCall, 1)
		dec.Decode(&reqs[0])
	}

	// Execute the requests
	resps, _ := call.Otto.Object("new Array()")
	for _, req := range reqs {
		resp, _ := call.Otto.Object(`({"jsonrpc": "2.0"})`)
		resp.Set("id", req.ID)
		var result json.RawMessage
		err = b.client.Call(&result, req.Method, req.Params...)
		switch err := err.(type) {
		case nil:
			if result == nil {
				resp.Set("result", otto.NullValue())
			} else {
				resultVal, err := JSON.Call("parse", string(result))
				if err != nil {
					setError(resp, -32603, err.Error())
				} else {
					resp.Set("result", resultVal)
				}
			}
		case rpc.Error:
			setError(resp, err.ErrorCode(), err.Error())
		default:
			setError(resp, -32603, err.Error())
		}

		resps.Call("push", resps)
	}

	if batch {
		response = resps.Value()
	} else {
		response, _ = resps.Get("0")
	}
	if fn := call.Argument(1); fn.Class() == "Function" {
		fn.Call(otto.NullValue(), otto.NullValue(), response)
		return otto.UndefinedValue()
	}
	return response
}

func setError(resp *otto.Object, code int, msg string) {
	resp.Set("error", map[string]interface{}{"code": code, "message": msg})
}

func throwJSException(msg interface{}) otto.Value {
	val, err := otto.ToValue(msg)
	if err != nil {
		log.Error("Failed to serialize JavaScript exception", "exception", msg, "err", err)
	}
	panic(val)
}
