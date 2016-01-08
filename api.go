// Package discover implements trimming of ASTs based on test coverage,
// to aid in conceptualizing large code bases.
//
// It is based on the idea presented by Alan Shreve in his talk on
// conceptualizing large software systems, held at dotGo 2015 in Paris.
package discover

import "github.com/eandre/discover/runtimeutil"

type D struct {
	traceID int
}

func (d *D) Track(f func()) {
	disable := runtimeutil.Enable(d.traceID)
	defer disable()

	f()
}
