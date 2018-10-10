package main

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"

	"golang.org/x/net/websocket"
)

func main() {
	//testChannel()
	//testNilChannel()
	//testVariant()
	//testMd5()
	//testDialWebsocket()
	testVariableParameter1()
	testVariableParameter2()
}

func testVariableParameter1() {

	var min int
	min = MinimumInt(10, 15, 32, 46, 2, 3)
	fmt.Printf("min = %v \n", min)

	var sliceInt = []int{10, 15, 32, 46, 2, 3}
	min = MinimumInt(sliceInt[0], sliceInt[1], sliceInt[2], sliceInt[3], sliceInt[4], sliceInt[5])
	fmt.Printf("min = %v \n", min)

	min = MinimumInt(sliceInt[0], sliceInt[1:]...)
	fmt.Printf("min = %v \n", min)
}

func MinimumInt(first int, others ...int) int {
	min := first
	for _, value := range others {
		if value < min {
			min = value
		}
	}
	return min
}

func testVariableParameter2() {
	var min interface{}
	min = Minimum(10, 15, 32, 46, 2, 3)
	fmt.Printf("min = %v \n", min)

	var sliceInt = []int{10, 15, 32, 46, 2, 3}
	min = Minimum(sliceInt[0], sliceInt[1], sliceInt[2], sliceInt[3], sliceInt[4], sliceInt[5])
	fmt.Printf("min = %v \n", min)

	min = Minimum(sliceInt[0], []interface{}{sliceInt[1], sliceInt[2], sliceInt[3], sliceInt[4], sliceInt[5]}...)
	fmt.Printf("min = %v \n", min)
}

func Minimum(first interface{}, rest ...interface{}) interface{} {
	min := first
	for _, value := range rest {
		switch value := value.(type) {
		case int:
			if value < min.(int) {
				min = value
			}
		case float64:
			if value < min.(float64) {
				min = value
			}
		case string:
			if value < min.(string) {
				min = value
			}
		}
	}

	return min
}

func testDialWebsocket() {
	var err error
	var conn net.Conn
	if conn, err = net.Dial("tcp", "127.0.0.1:8545"); err != nil {
		panic(err)
	}

	var wsConfig *websocket.Config
	if wsConfig, err = websocket.NewConfig("ws://127.0.0.1:8545", "*"); err != nil {
		panic(err)
	}

	var wsConn *websocket.Conn
	if wsConn, err = websocket.NewClient(wsConfig, conn); err != nil {
		panic(err)
	}

	var msg = map[string]interface{}{"Version": "2.0", "ID": []byte("0"), "Method": "svc_test", "Params": []byte{}}
	if err = json.NewEncoder(wsConn).Encode(msg); err != nil {
		panic(err)
	}

	var n = 0
	var buf = make([]byte, 1280)
	if n, err = conn.Read(buf); err != nil {
		panic(err)
	}
	fmt.Println(string(buf[:n]))
}

func testMd5() {
	md5hasher := md5.New()
	f, err := os.OpenFile("E:\\迅雷下载\\movie\\111.rmvb", os.O_RDONLY, 0666)
	if err != nil {
		panic(err)
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Println(ms.Frees)

	n, err := io.Copy(md5hasher, f)
	if err != nil {
		panic(err)
	}
	md5hash := md5hasher.Sum(nil)

	runtime.ReadMemStats(&ms)
	fmt.Println(ms.Frees)

	fmt.Printf("写入 %v 字节 \n", n)
	fmt.Printf("MD5 = 0x%x", md5hash)
}

func testVariant() {
	blob := make([]byte, binary.MaxVarintLen64)
	fmt.Printf("blob size = %v \n", len(blob))

	n := binary.PutVarint(blob, int64(50))
	fmt.Printf("blob size = %v \n", len(blob))

	blob = blob[:n]
	fmt.Printf("blob size = %v \n", len(blob))
}

func testNilChannel() {
	var c1 chan struct{}
	c1 = nil

	go func() {
		select {
		case <-c1: // program will be blocked at this
			fmt.Println("I got notification from c1.")
		}
	}()

	fmt.Println("ok!")
}

func testChannel() {
	var c1 chan struct{}
	c1 = make(chan struct{})

	go func() {
		select {
		case <-c1:
			fmt.Println("I got notification from c1.")
		}
	}()

	c1 <- struct{}{}
	fmt.Println("ok!")
}
