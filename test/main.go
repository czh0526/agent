package main

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"runtime"
)

func main() {
	//testChannel()
	//testNilChannel()
	//testVariant()
	testMd5()
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
