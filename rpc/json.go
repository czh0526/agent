package rpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

const (
	jsonrpcVersion         = "2.0"
	serviceMethodSeparator = "_"
)

type jsonRequest struct {
	Method  string          `json:"method"`
	Version string          `json:"jsonrpc"`
	Id      json.RawMessage `json:"id,omitempty"`
	Payload json.RawMessage `json:"params,omitempty"`
}

type jsonSuccessResponse struct {
	Version string      `json:"jsonrpc"`
	Id      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result"`
}

type jsonError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func (err *jsonError) Error() string {
	if err.Message == "" {
		return fmt.Sprintf("json-rpc error %d", err.Code)
	}
	return err.Message
}

func (err *jsonError) ErrorCode() int {
	return err.Code
}

type jsonErrResponse struct {
	Version string      `json:"jsonrpc"`
	Id      interface{} `json:"id,omitempty"`
	Error   jsonError   `json:"error"`
}

type jsonCodec struct {
	closer sync.Once
	closed chan interface{}
	decode func(v interface{}) error
	encode func(v interface{}) error
	rw     io.ReadWriteCloser
}

func (c *jsonCodec) ReadRequestHeaders() ([]rpcRequest, bool, Error) {
	var incomingMsg json.RawMessage
	if err := c.decode(&incomingMsg); err != nil {
		return nil, false, &invalidRequestError{err.Error()}
	}
	// jsonRawMessage ==> rpcRequest
	return parseRequest(incomingMsg)
}

func parseRequest(incomingMsg json.RawMessage) ([]rpcRequest, bool, Error) {
	var in jsonRequest
	if err := json.Unmarshal(incomingMsg, &in); err != nil {
		return nil, false, &invalidMessageError{err.Error()}
	}

	if err := checkReqId(in.Id); err != nil {
		return nil, false, &invalidMessageError{err.Error()}
	}

	elems := strings.Split(in.Method, serviceMethodSeparator)
	if len(elems) != 2 {
		return nil, false, &methodNotFoundError{in.Method, ""}
	}

	if len(in.Payload) == 0 {
		return []rpcRequest{{service: elems[0], method: elems[1], id: &in.Id}}, false, nil
	}

	return []rpcRequest{{service: elems[0], method: elems[1], id: &in.Id, params: in.Payload}}, false, nil
}

func (c *jsonCodec) ParseRequestArguments(argTypes []reflect.Type, params interface{}) ([]reflect.Value, Error) {
	if args, ok := params.(json.RawMessage); !ok {
		return nil, &invalidParamsError{"Invalid params supplied"}
	} else {
		return parsePositionalArguments(args, argTypes)
	}
}

func parsePositionalArguments(rawArgs json.RawMessage, types []reflect.Type) ([]reflect.Value, Error) {
	dec := json.NewDecoder(bytes.NewReader(rawArgs))
	if tok, _ := dec.Token(); tok != json.Delim('[') {
		return nil, &invalidParamsError{"non-array args"}
	}

	args := make([]reflect.Value, 0, len(types))
	for i := 0; dec.More(); i++ {
		if i >= len(types) {
			return nil, &invalidParamsError{fmt.Sprintf("too many arguments, want at most %d", len(types))}
		}
		argval := reflect.New(types[i])
		if err := dec.Decode(argval.Interface()); err != nil {
			return nil, &invalidParamsError{fmt.Sprintf("invalid argument %d: %v", i, err)}
		}

		if argval.IsNil() && types[i].Kind() != reflect.Ptr {
			return nil, &invalidParamsError{fmt.Sprintf("missing value for required argument %d", i)}
		}
		args = append(args, argval.Elem())
	}

	if _, err := dec.Token(); err != nil {
		return nil, &invalidParamsError{err.Error()}
	}

	for i := len(args); i < len(types); i++ {
		if types[i].Kind() != reflect.Ptr {
			return nil, &invalidParamsError{fmt.Sprintf("missing value for required argument %d", i)}
		}
		args = append(args, reflect.Zero(types[i]))
	}
	return args, nil
}

func checkReqId(reqId json.RawMessage) error {
	if len(reqId) == 0 {
		return fmt.Errorf("missing request id")
	}
	if _, err := strconv.ParseFloat(string(reqId), 64); err == nil {
		return nil
	}
	var str string
	if err := json.Unmarshal(reqId, &str); err == nil {
		return nil
	}
	return fmt.Errorf("invalid request id")
}

func (c *jsonCodec) CreateResponse(id interface{}, reply interface{}) interface{} {
	if isHexNum(reflect.TypeOf(reply)) {
		return &jsonSuccessResponse{
			Version: jsonrpcVersion,
			Id:      id,
			Result:  fmt.Sprintf(`%#x`, reply),
		}
	}
	return &jsonSuccessResponse{
		Version: jsonrpcVersion,
		Id:      id,
		Result:  reply,
	}
}

func (c *jsonCodec) CreateErrorResponse(id interface{}, err Error) interface{} {
	return &jsonErrResponse{
		Version: jsonrpcVersion,
		Id:      id,
		Error:   jsonError{Code: err.ErrorCode(), Message: err.Error()}}
}

func (c *jsonCodec) Write(res interface{}) error {
	return c.encode(res)
}

func (c *jsonCodec) Close() {
	c.closer.Do(func() {
		close(c.closed)
		c.rw.Close()
	})
}

func NewCodec(rwc io.ReadWriteCloser, encode, decode func(v interface{}) error) ServerCodec {
	return &jsonCodec{
		closed: make(chan interface{}),
		encode: encode,
		decode: decode,
		rw:     rwc,
	}
}

func NewJSONCodec(rwc io.ReadWriteCloser) ServerCodec {
	enc := json.NewEncoder(rwc)
	dec := json.NewDecoder(rwc)
	dec.UseNumber()

	return &jsonCodec{
		closed: make(chan interface{}),
		encode: enc.Encode,
		decode: dec.Decode,
		rw:     rwc,
	}
}

func isBatch(msg json.RawMessage) bool {
	for _, c := range msg {
		if c == 0x20 || c == 0x09 || c == 0x0a || c == 0x0d {
			continue
		}
		return c == '['
	}
	return false
}
