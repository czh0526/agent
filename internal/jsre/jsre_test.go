package jsre

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"testing"
	"time"

	"github.com/robertkrimen/otto"
)

func newWithTestJS(t *testing.T, testjs string) (*JSRE, string) {
	dir, err := ioutil.TempDir("", "jsre-test")
	if err != nil {
		t.Fatal("cannot create temporary directory:", err)
	}
	if testjs != "" {
		if err := ioutil.WriteFile(path.Join(dir, "test.js"), []byte(testjs), os.ModePerm); err != nil {
			t.Fatal("cannot create test.js:", err)
		}
	}
	return New(dir, os.Stdout), dir
}

func TestExec(t *testing.T) {
	jsre, dir := newWithTestJS(t, `msg = "testMsg"`)
	defer os.RemoveAll(dir)

	err := jsre.Exec("test.js")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	val, err := jsre.Run("msg")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if !val.IsString() {
		t.Errorf("expected string value, got %v", val)
	}

	exp := "testMsg"
	got, _ := val.ToString()
	if exp != got {
		t.Errorf("expected '%v', got '%v'", exp, got)
	}
	jsre.Stop(false)
}

func TestNatto(t *testing.T) {
	jsre, dir := newWithTestJS(t, `setTimeout(function() { msg = "testMsg"}, 100)`)
	defer os.RemoveAll(dir)

	err := jsre.Exec("test.js")
	if err != nil {
		panic(err)
	}
	time.Sleep(100 * time.Millisecond)
	val, err := jsre.Run("msg")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	fmt.Println(val)
}

func TestCompile(t *testing.T) {
	vm := otto.New()
	script, err := vm.Compile("testdata/my.js", nil)
	if err != nil {
		panic(err)
	}
	fmt.Println(script)

	c, err := vm.Get("c")
	if err != nil {
		panic(err)
	}

	_, err = vm.Run(script)
	if err != nil {
		panic(err)
	}

	c, err = vm.Get("c")
	if err != nil {
		panic(err)
	}

	fmt.Println(c)
}
