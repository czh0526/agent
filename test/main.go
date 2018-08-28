package main

import "fmt"

func main() {

	var c1 chan struct{}
	c1 = nil
	//c1 = make(chan struct{})

	go func() {
		select {
		case <-c1:
			fmt.Println("I got notification from c1.")
		}
	}()

	//c1 <- struct{}{}
	fmt.Println("ok!")
}
