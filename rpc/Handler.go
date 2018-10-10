package rpc

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/czh0526/agent/log"
	set "gopkg.in/fatih/set.v0"
)

const MetadataApi = "rpc"

type serviceRegistry map[string]*service
type callbacks map[string]*callback

type CodecOption int

const (
	OptionMethodInvocation CodecOption = 1 << iota
)

type Handler struct {
	services serviceRegistry
	run      int32
	codecs   *set.Set
}

func NewHandler() *Handler {
	handler := &Handler{
		services: make(serviceRegistry),
		run:      1,
		codecs:   set.New(),
	}
	rpcService := &RPCService{handler}
	handler.RegisterName(MetadataApi, rpcService)
	return handler
}

// 模式存在的 RPC 服务，用于枚举全部 services
type RPCService struct {
	handler *Handler
}

func (s *RPCService) Modules() map[string]string {
	modules := make(map[string]string)
	for name, _ := range s.handler.services {
		modules[name] = "1.0"
	}
	return modules
}

func (self *Handler) Stop() error {
	if atomic.CompareAndSwapInt32(&self.run, 1, 0) {
		self.codecs.Each(func(c interface{}) bool {
			c.(ServerCodec).Close()
			return true
		})
	}
	return nil
}

func (s *Handler) RegisterName(name string, rcvr interface{}) error {
	if s.services == nil {
		s.services = make(serviceRegistry)
	}
	svc := new(service)
	svc.typ = reflect.TypeOf(rcvr)
	rcvrVal := reflect.ValueOf(rcvr)

	if name == "" {
		return fmt.Errorf("no service name for type %s", svc.typ.String())
	}
	if !isExported(reflect.Indirect(rcvrVal).Type().Name()) {
		return fmt.Errorf("%s is not exported", reflect.Indirect(rcvrVal).Type().Name())
	}

	methods := suitableCallbacks(rcvrVal, svc.typ)
	if regsvc, present := s.services[name]; present {
		if len(methods) == 0 {
			return fmt.Errorf("Service %T doesn't have any suitable methods to expose", rcvr)
		}
		for _, m := range methods {
			regsvc.callbacks[formatName(m.method.Name)] = m
		}
		return nil
	}

	svc.name = name
	svc.callbacks = methods

	if len(svc.callbacks) == 0 {
		return fmt.Errorf("Service %T doesn't have any suitable methods to expose", rcvr)
	}

	s.services[svc.name] = svc
	return nil
}

// 为监听端口绑定协议处理器
func (s *Handler) ServeListener(l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}

		go s.ServeCodec(NewJSONCodec(conn), OptionMethodInvocation)
	}
}

func (s *Handler) ServeCodec(codec ServerCodec, options CodecOption) {
	defer codec.Close()
	s.serveRequest(context.Background(), codec, false, options)
}

func (s *Handler) serveRequest(ctx context.Context, codec ServerCodec, singleShot bool, options CodecOption) error {
	var pend sync.WaitGroup

	defer func() {

	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// 将 codec 纳入管理
	s.codecs.Add(codec)

	for atomic.LoadInt32(&s.run) == 1 {
		// 读取 serverRequest
		reqs, batch, err := s.readRequest(codec)
		if err != nil {
			if err.Error() != "EOF" {
				fmt.Printf("read error %v \n", err)
				codec.Write(codec.CreateErrorResponse(nil, err))
			}
			pend.Wait()
			return nil
		}

		pend.Add(1)
		// 单开一个例程，处理 serverRequest
		go func(reqs []*serverRequest, batch bool) {
			defer pend.Done()
			if batch {
				s.execBatch(ctx, codec, reqs)
			} else {
				s.exec(ctx, codec, reqs[0])
			}
		}(reqs, batch)
	}
	return nil
}

func (s *Handler) exec(ctx context.Context, codec ServerCodec, req *serverRequest) {
	var response interface{}
	if req.err != nil {
		response = codec.CreateErrorResponse(&req.id, req.err)
	} else {
		response, _ = s.handle(ctx, codec, req)
	}

	if err := codec.Write(response); err != nil {
		log.Error(fmt.Sprintf("%v \n", err))
		codec.Close()
	}
}

func (s *Handler) execBatch(ctx context.Context, codec ServerCodec, requests []*serverRequest) {
	responses := make([]interface{}, len(requests))
	for i, req := range requests {
		if req.err != nil {
			responses[i] = codec.CreateErrorResponse(&req.id, req.err)
		} else {
			responses[i], _ = s.handle(ctx, codec, req)
		}
	}

	if err := codec.Write(responses); err != nil {
		codec.Close()
	}
}

func (s *Handler) handle(ctx context.Context, codec ServerCodec, req *serverRequest) (interface{}, func()) {
	if req.err != nil {
		return codec.CreateErrorResponse(&req.id, req.err), nil
	}

	if len(req.args) != len(req.callb.argTypes) {
		rpcErr := &invalidParamsError{
			fmt.Sprintf("%s%s%s expects %d parameters, got %d",
				req.svcname, serviceMethodSeparator, req.callb.method.Name,
				len(req.callb.argTypes), len(req.args))}
		return codec.CreateErrorResponse(&req.id, rpcErr), nil
	}

	arguments := []reflect.Value{req.callb.rcvr}
	if req.callb.hasCtx {
		arguments = append(arguments, reflect.ValueOf(ctx))
	}
	if len(req.args) > 0 {
		arguments = append(arguments, req.args...)
	}
	reply := req.callb.method.Func.Call(arguments)
	if len(reply) == 0 {
		return codec.CreateResponse(req.id, nil), nil
	}

	if req.callb.errPos >= 0 {
		if reply[req.callb.errPos].IsNil() {
			e := reply[req.callb.errPos].Interface().(Error)
			res := codec.CreateErrorResponse(&req.id, &callbackError{e.Error()})
			return res, nil
		}
	}

	return codec.CreateResponse(req.id, reply[0].Interface()), nil
}

func (s *Handler) readRequest(codec ServerCodec) ([]*serverRequest, bool, Error) {
	// 读取 rpcRequest
	reqs, batch, err := codec.ReadRequestHeaders()
	if err != nil {
		return nil, batch, err
	}

	// rpcRequest ==> serverRequest
	requests := make([]*serverRequest, len(reqs))
	for i, r := range reqs {
		var ok bool
		var svc *service

		if r.err != nil {
			requests[i] = &serverRequest{id: r.id, err: r.err}
			continue
		}

		if svc, ok = s.services[r.service]; !ok {
			requests[i] = &serverRequest{id: r.id, err: &methodNotFoundError{r.service, r.method}}
			continue
		}

		if callb, ok := svc.callbacks[r.method]; ok {
			requests[i] = &serverRequest{id: r.id, svcname: svc.name, callb: callb}
			// 提取 callback 的参数
			if r.params != nil && len(callb.argTypes) > 0 {
				requests[i] = &serverRequest{id: r.id, svcname: svc.name, callb: callb}
				if r.params != nil && len(callb.argTypes) > 0 {
					argTypes := []reflect.Type{}
					argTypes = append(argTypes, callb.argTypes...)
					if args, err := codec.ParseRequestArguments(argTypes, r.params); err == nil {
						requests[i].args = args[:] // first one is service.method name which isn't an actual argument
					} else {
						requests[i].err = &invalidParamsError{err.Error()}
					}
				}
			}
			continue
		}

		requests[i] = &serverRequest{id: r.id, err: &methodNotFoundError{r.service, r.method}}
	}

	return requests, batch, nil
}
