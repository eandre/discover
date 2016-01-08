package runtimeutil

import "fmt"

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
