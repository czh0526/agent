package rpc

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

type MyError struct {
}

func (t MyError) Error() string {
	return "MyError"
}

func TestErrorType(t *testing.T) {
	myErr := new(MyError)
	myErrType := reflect.TypeOf(myErr)
	fmt.Println(isErrorType(myErrType))
}

func TestContextType(t *testing.T) {
	//ctx := (*context.Context)(nil)
	ctx := context.Background()
	ctxType := reflect.TypeOf(ctx)
	fmt.Printf("ctx type = %v \n", ctxType.Kind())
	fmt.Println(isContextType(ctxType))
}

type Cat interface {
	SetName(string)
	Mow() string
}

type Tabby struct {
	name string
}

func (t *Tabby) SetName(name string) {
	t.name = name
}

func (t *Tabby) Mow() string {
	return fmt.Sprintf("Mow from %v !", t.name)
}

type Gafield struct {
	name string
}

func (g Gafield) SetName(name string) {
	g.name = name
}

func (g Gafield) Mow() string {
	return fmt.Sprintf("Mow from %v !", g.name)
}

func TestCat(t *testing.T) {
	var cat Cat
	var catType reflect.Type
	fmt.Println("接口类型的变量，一旦被赋值, type = 具体被赋值的类, kind = struct|ptr")

	fmt.Println("struct ==> ")
	cat = Gafield{name: "Gafield-1"}
	catType = reflect.TypeOf(cat)
	fmt.Printf("%v, %v \n", catType, catType.Kind())
	fmt.Println()

	fmt.Println("ptr ==> ")
	cat = &Tabby{name: "Tabby-1"}
	catType = reflect.TypeOf(cat)
	fmt.Printf("%v, %v \n", catType, catType.Kind())
	fmt.Println()

	fmt.Println("通过指向接口的指针，构建 interface 接口对象类型")
	catType = reflect.TypeOf((*Cat)(nil)).Elem()
	fmt.Printf("%v, %v \n", catType, catType.Kind())

}
