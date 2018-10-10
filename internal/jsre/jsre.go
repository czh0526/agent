package jsre

import (
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"time"

	"github.com/czh0526/agent/internal/jsre/deps"
	"github.com/ethereum/go-ethereum/common"
	"github.com/robertkrimen/otto"
)

var (
	BigNumber_JS = deps.MustAsset("bignumber.js")
	Web3_JS      = deps.MustAsset("web3.js")
)

type JSRE struct {
	assetPath     string
	output        io.Writer
	evalQueue     chan *evalReq
	stopEventLoop chan bool
	closed        chan struct{}
}

type jsTimer struct {
	timer    *time.Timer
	duration time.Duration
	interval bool
	call     otto.FunctionCall
}

type evalReq struct {
	fn   func(vm *otto.Otto)
	done chan bool
}

func New(assetPath string, output io.Writer) *JSRE {
	re := &JSRE{
		assetPath:     assetPath,
		output:        output,
		closed:        make(chan struct{}),
		evalQueue:     make(chan *evalReq),
		stopEventLoop: make(chan bool),
	}
	go re.runEventLoop()
	return re
}

func (re *JSRE) runEventLoop() {
	defer close(re.closed)

	// 构建一个 JavaScript 执行环境
	vm := otto.New()
	r := randomSource()
	vm.SetRandomSource(r.Float64)

	registry := map[*jsTimer]*jsTimer{}
	ready := make(chan *jsTimer)

	// 默认 call 接收 两个参数：1) func, 2) delay
	newTimer := func(call otto.FunctionCall, interval bool) (*jsTimer, otto.Value) {
		secondArg := call.Argument(1)
		delay, _ := secondArg.ToInteger()
		if 0 >= delay {
			delay = 1
		}
		timer := &jsTimer{
			duration: time.Duration(delay) * time.Millisecond,
			call:     call,
			interval: interval,
		}
		registry[timer] = timer

		// 定时器超时，向 eventLoop 发送处理信号
		timer.timer = time.AfterFunc(timer.duration, func() {
			ready <- timer
		})

		value, err := call.Otto.ToValue(timer)
		if err != nil {
			panic(err)
		}

		return timer, value
	}
	setTimeout := func(call otto.FunctionCall) otto.Value {
		_, value := newTimer(call, false)
		return value
	}

	vm.Set("_setTimeout", setTimeout)
	_, err := vm.Run(`var setTimeout = function(args) {
		if (arguments.length < 1) {
			throw TypeError("Failed to execute 'setTimeout': 1 argument required, but only 0 present.");
		}
		return _setTimeout.apply(this, arguments);
	}`)
	if err != nil {
		panic(err)
	}

	var waitForCallbacks bool
loop:
	for {
		select {

		// 调用定时器到期的函数
		case timer := <-ready:
			var arguments []interface{}
			if len(timer.call.ArgumentList) > 2 {
				tmp := timer.call.ArgumentList[2:]
				arguments = make([]interface{}, 2+len(tmp))
				for i, value := range tmp {
					arguments[i+2] = value
				}
			} else {
				arguments = make([]interface{}, 1)
			}
			// Javascript 中指定的定时器函数
			arguments[0] = timer.call.ArgumentList[0]
			_, err := vm.Call(`Function.call.call`, nil, arguments...)
			if err != nil {
				fmt.Println("js error:", err, arguments)
			}

			_, inreg := registry[timer]
			if timer.interval && inreg {
				timer.timer.Reset(timer.duration)
			} else {
				delete(registry, timer)
				if waitForCallbacks && (len(registry) == 0) {
					break loop
				}
			}

		// 调度一个执行请求
		case req := <-re.evalQueue:
			// 在 vm 中执行函数
			req.fn(vm)
			// 通知等待协程"执行完毕"
			close(req.done)
			// 判断是否应该退出循环
			if waitForCallbacks && (len(registry) == 0) {
				break loop
			}

		// 收到外部的终止信号
		case waitForCallbacks = <-re.stopEventLoop:
			if !waitForCallbacks || len(registry) == 0 {
				break loop
			}
		}
	}

	for _, timer := range registry {
		timer.timer.Stop()
		delete(registry, timer)
	}
}

// 终止 eventLoop
func (re *JSRE) Stop(waitForCallbacks bool) {
	select {
	case <-re.closed:
	case re.stopEventLoop <- waitForCallbacks:
		<-re.closed
	}
}

// 执行给定的 JavaScript 代码文件
func (re *JSRE) Exec(file string) error {
	// 读取代码
	code, err := ioutil.ReadFile(common.AbsolutePath(re.assetPath, file))
	if err != nil {
		return err
	}

	// 执行代码
	var script *otto.Script
	re.Do(func(vm *otto.Otto) {
		script, err = vm.Compile(file, code)
		if err != nil {
			return
		}
		_, err = vm.Run(script)
	})

	return err
}

func (re *JSRE) Run(code string) (v otto.Value, err error) {
	re.Do(func(vm *otto.Otto) {
		v, err = vm.Run(code)
	})
	return v, err
}

func (re *JSRE) Compile(filename string, src interface{}) (err error) {
	re.Do(func(vm *otto.Otto) {
		_, err = compileAndRun(vm, filename, src)
	})
	return err
}

func compileAndRun(vm *otto.Otto, filename string, src interface{}) (otto.Value, error) {
	script, err := vm.Compile(filename, src)
	if err != nil {
		return otto.Value{}, err
	}
	return vm.Run(script)
}

func (re *JSRE) Evaluate(code string, w io.Writer) error {
	var fail error

	re.Do(func(vm *otto.Otto) {
		val, fail := vm.Run(code)
		if fail != nil {
			prettyError(vm, fail, w)
		} else {
			prettyPrint(vm, val, w)
		}
	})
	return fail
}

func (re *JSRE) Set(ns string, v interface{}) (err error) {
	re.Do(func(vm *otto.Otto) {
		err = vm.Set(ns, v)
	})
	return err
}

func (re *JSRE) Get(ns string) (v otto.Value, err error) {
	re.Do(func(vm *otto.Otto) {
		v, err = vm.Get(ns)
	})
	return v, err
}

// 请求执行函数 fn
func (re *JSRE) Do(fn func(*otto.Otto)) {
	done := make(chan bool)
	// 构建一个执行请求
	req := &evalReq{fn, done}
	// 写入 jsre 请求队列
	re.evalQueue <- req
	// 等待 jsre 处理完毕
	<-done
}

func randomSource() *rand.Rand {
	bytes := make([]byte, 9)
	seed := time.Now().UnixNano()
	if _, err := crand.Read(bytes); err == nil {
		seed = int64(binary.LittleEndian.Uint64(bytes))
	}

	src := rand.NewSource(seed)
	return rand.New(src)
}
