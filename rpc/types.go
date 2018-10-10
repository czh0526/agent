package rpc

import "reflect"

type API struct {
	Namespace string
	Version   string
	Service   interface{}
	Public    bool
}

type callback struct {
	rcvr        reflect.Value
	method      reflect.Method
	argTypes    []reflect.Type
	hasCtx      bool
	errPos      int
	isSubscribe bool
}

type service struct {
	name      string
	typ       reflect.Type
	callbacks callbacks
}
type rpcRequest struct {
	service string
	method  string
	id      interface{}
	params  interface{}
	err     Error
}

type serverRequest struct {
	id      interface{}
	svcname string
	callb   *callback
	args    []reflect.Value
	err     Error
}

type Error interface {
	Error() string
	ErrorCode() int
}

type ServerCodec interface {
	ReadRequestHeaders() ([]rpcRequest, bool, Error)
	ParseRequestArguments(argTypes []reflect.Type, params interface{}) ([]reflect.Value, Error)
	Write(msg interface{}) error
	CreateResponse(id interface{}, reply interface{}) interface{}
	CreateErrorResponse(id interface{}, err Error) interface{}
	Close()
}
