package runtimeutil

import "reflect"

// MakeFunc creates a closure that calls f with args. It exists to
// aid the implementation of goroutine tracking, where we need to preserve
// the property that the function parameters are evaluated immediately.
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

// MakeVariadicFunc is like MakeFunc, except that the last entry in args
// is passed as the variadic parameter to f.
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
