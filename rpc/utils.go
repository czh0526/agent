package rpc

import (
	"context"
	"math/big"
	"reflect"
	"unicode"
	"unicode/utf8"
)

// 接口是一个(type, val)的二元组，必须通过这个方式取得类型
var errorType = reflect.TypeOf((*error)(nil)).Elem()

func isErrorType(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.Implements(errorType)
}

var contextType = reflect.TypeOf((*context.Context)(nil)).Elem()

func isContextType(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.Implements(contextType)
}

var bigIntType = reflect.TypeOf((*big.Int)(nil)).Elem()

func isHexNum(t reflect.Type) bool {
	if t == nil {
		return false
	}
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t == bigIntType
}

func isExported(name string) bool {
	rune, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(rune)
}

func isExportedOrBuiltinType(t reflect.Type) bool {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return isExported(t.Name()) || t.PkgPath() == ""
}

func formatName(name string) string {
	ret := []rune(name)
	if len(ret) > 0 {
		ret[0] = unicode.ToLower(ret[0])
	}
	return string(ret)
}

func suitableCallbacks(rcvr reflect.Value, typ reflect.Type) callbacks {
	callbacks := make(callbacks)

METHODS:
	for m := 0; m < typ.NumMethod(); m++ {
		method := typ.Method(m)
		mtype := method.Type
		mname := formatName(method.Name)
		if method.PkgPath != "" {
			continue
		}

		var h callback
		h.rcvr = rcvr
		h.method = method
		h.errPos = -1

		// 检查函数的第一个参数
		firstArg := 1
		numIn := mtype.NumIn()
		if numIn >= 2 && mtype.In(1) == contextType {
			h.hasCtx = true
			firstArg = 2
		}

		// 从 firstArg 开始，收集入参的类型
		h.argTypes = make([]reflect.Type, numIn-firstArg)
		for i := firstArg; i < numIn; i++ {
			argType := mtype.In(i)
			if !isExportedOrBuiltinType(argType) {
				continue METHODS
			}
			h.argTypes[i-firstArg] = argType
		}

		// 检查出参的类型
		for i := 0; i < mtype.NumOut(); i++ {
			if !isExportedOrBuiltinType(mtype.Out(i)) {
				continue METHODS
			}
		}

		// 检查 error 参数
		h.errPos = -1
		for i := 0; i < mtype.NumOut(); i++ {
			if isErrorType(mtype.Out(i)) {
				h.errPos = i
				break
			}
		}
		// error 必须是最后一个出参
		if h.errPos >= 0 && h.errPos != mtype.NumOut()-1 {
			continue METHODS
		}

		// 通过全部检查的函数，设置 callbacks
		switch mtype.NumOut() {
		case 0, 1, 2:
			if mtype.NumOut() == 2 && h.errPos == -1 {
				continue METHODS
			}
			callbacks[mname] = &h
		}
	}

	return callbacks
}
