package runtimeutil

import (
	"bytes"
	"runtime"
	"strconv"
)

// gid returns the id for the running goroutine.
func gid() uint64 {
	b := make([]byte, 64)
	b = b[:runtime.Stack(b, false)]
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	b = b[:bytes.IndexByte(b, ' ')]
	n, err := strconv.ParseUint(string(b), 10, 64)
	if err != nil {
		panic("discover: Could not parse registry ID: " + err.Error())
	}
	return n
}
