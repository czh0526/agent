package main

import (
	"encoding/binary"
	"fmt"
)

func main() {
	//testChannel()
	//testNilChannel()
	testVariant()
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
