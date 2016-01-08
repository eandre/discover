package runtimeutil

import (
	"fmt"
	"reflect"
)

func Enable(traceID int) func() {
	id := enable()
	return func() {
		disable(id)
	}
}

func TraceID() int {
	return 0
}

func enable() uint64 {
	id := gid()
	fmt.Println("Enabling", id)
	return id
}

func disable(id uint64) {
	fmt.Println("Disabling", id)
}

func ChildEnable(traceID int) {
	enable()
}

func MakeFunc(f interface{}, args ...interface{}) func() {
	val := reflect.ValueOf(f)
	if val.Kind() != reflect.Func {
		panic("discover: runtime error: MakeFunc() got non-func argument")
	}

	in := make([]reflect.Value, len(args))
	for i, arg := range args {
		in[i] = reflect.ValueOf(arg)
	}

	return func() {
		val.Call(in)
	}
}

func MakeVariadicFunc(f interface{}, args ...interface{}) func() {
	val := reflect.ValueOf(f)
	if val.Kind() != reflect.Func {
		panic("discover: runtime error: MakeVariadicFunc() got non-func argument")
	}

	in := make([]reflect.Value, len(args))
	for i, arg := range args {
		in[i] = reflect.ValueOf(arg)
	}

	return func() {
		val.CallSlice(in)
	}
}
