package main

import (
	"log"
	"time"

	"github.com/eandre/discover"
)

func Foo() {
	log.Println("running Foo()")
	go func() {
		// this is also tracked
		log.Println("running goroutine")
	}()

	go func(foo ...interface{}) {
		// this is also tracked
		log.Println(foo...)
	}("running in goroutine", "with variadic args")

	args := []interface{}{"running", "with", "ellipsis"}
	go func(fmt string, foo ...interface{}) {
		// this is also tracked
		log.Printf(fmt, foo...)
	}("%s: %s %s", args...)
}

func main() {
	DiscoverClient(&discover.D{})
	time.Sleep(500 * time.Millisecond)
}
