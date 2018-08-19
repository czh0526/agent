package main

type Agent interface {
	Start() error
	Stop() error
}
