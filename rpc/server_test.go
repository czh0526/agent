package rpc

import (
	"context"
	"encoding/json"
	"net"
	"reflect"
	"testing"
)

type Args struct {
	S string
}

type Result struct {
	String string
	Int    int
	Args   *Args
}

type Svc struct{}

func (s *Svc) Echo(str string, i int, args *Args) Result {
	return Result{str, i, args}
}

func (s *Svc) EchoWithCtx(ctx context.Context, str string, i int, args *Args) Result {
	return Result{str, i, args}
}

func TestHandlerRegisterName(t *testing.T) {
	handler := NewHandler()
	service := new(Svc)

	if err := handler.RegisterName("calc", service); err != nil {
		t.Fatalf("%v", err)
	}

	if len(handler.services) != 2 {
		t.Fatalf("Expected 2 service entries, got %d", len(handler.services))
	}

	svc, ok := handler.services["calc"]
	if !ok {
		t.Fatalf("Expected service calc to be registered")
	}

	if len(svc.callbacks) != 2 {
		t.Errorf("Expected 5 callbacks for service 'calc', got %d", len(svc.callbacks))
	}
}

func TestHandlerMethodExecution(t *testing.T) {
	testHandlerMethodExecution(t, "echo")
}

func TestHandlerMethodWithCtx(t *testing.T) {
	testHandlerMethodExecution(t, "echoWithCtx")
}

func testHandlerMethodExecution(t *testing.T, method string) {
	handler := NewHandler()
	svc := new(Svc)

	if err := handler.RegisterName("test", svc); err != nil {
		t.Fatalf("%v", err)
	}

	stringArg := "string arg"
	intArg := 1122
	argsArg := &Args{"abcde"}
	params := []interface{}{stringArg, intArg, argsArg}

	request := map[string]interface{}{
		"id":      12345,
		"method":  "test_" + method,
		"jsonrpc": "2.0",
		"params":  params,
	}

	// 构建一条链接
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	// 将链接的一端交给 server
	go handler.ServeCodec(NewJSONCodec(serverConn), OptionMethodInvocation)

	// 为客户端的输入/出配置编/解码器
	out := json.NewEncoder(clientConn)
	in := json.NewDecoder(clientConn)

	// 发送 request
	if err := out.Encode(request); err != nil {
		t.Fatal(err)
	}

	// 接收 response
	response := jsonSuccessResponse{Result: &Result{}}
	if err := in.Decode(&response); err != nil {
		t.Fatal(err)
	}

	// 校验函数调用的返回值
	if result, ok := response.Result.(*Result); ok {
		if result.String != stringArg {
			t.Errorf("expected '%s', got: '%s' \n", stringArg, result.String)
		}

		if result.Int != intArg {
			t.Errorf("expected '%d', got '%d' \n", intArg, result.Int)
		}

		if !reflect.DeepEqual(result.Args, argsArg) {
			t.Errorf("expected '%v', got '%v' \n", argsArg, result)
		}
	} else {
		t.Fatalf("invalid response: expected *Result - got: %T", response.Result)
	}
}
