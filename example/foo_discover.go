package main

import "github.com/eandre/discover"

func DiscoverClient(d *discover.D) {
	d.Track(func() {
		Foo()
	})
}
