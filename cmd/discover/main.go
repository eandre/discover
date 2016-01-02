package main

import (
	"fmt"
	"go/printer"
	"log"
	"os"

	"github.com/eandre/discover"
	"golang.org/x/tools/cover"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s path/to/coverprofile\n", os.Args[0])
		os.Exit(1)
	}

	fileName := os.Args[1]
	profiles, err := cover.ParseProfiles(fileName)
	if err != nil {
		log.Fatal(err)
	}

	prof, err := discover.ParseProfile(profiles)
	if err != nil {
		log.Fatal(err)
	}

	for _, f := range prof.Files {
		prof.Trim(f)

		// If we filtered out all decls, don't print at all
		if len(f.Decls) == 0 {
			continue
		}

		printer.Fprint(os.Stdout, prof.Fset, f)
		fmt.Println("\n\n")
	}
}
