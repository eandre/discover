package main

import (
	"errors"
	"flag"
	"fmt"
	"go/printer"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/eandre/discover"
	"golang.org/x/tools/cover"
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: \n\ndiscover command [arguments\n\n")
	fmt.Fprintf(os.Stderr, "The commands are:")
	fmt.Fprintf(os.Stderr, "\tdiscover test <test name> [<test name>...]")
	fmt.Fprintf(os.Stderr, "\tdiscover parse <cover profile>")
}

func main() {
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() == 0 {
		usage()
		os.Exit(1)
	}

	switch flag.Arg(0) {
	case "test":
		// run tests
		if err := runTests(flag.Arg(1)); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}

	case "parse":
		if flag.NArg() <= 1 {
			fmt.Fprintln(os.Stderr, "missing cover profile")
			os.Exit(1)
		}
		if err := parseProfile(flag.Arg(1)); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	}
}

func runTests(testRegexp string) error {
	tmpDir, err := ioutil.TempDir("", "discover")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	profilePath := filepath.Join(tmpDir, "coverprofile.out")
	args := []string{"test", "-coverprofile", profilePath}
	if testRegexp != "" {
		args = append(args, "-run", testRegexp)
	}

	cmd := exec.Command("go", args...)
	cmd.Stdin = nil
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	if _, err := os.Stat(profilePath); os.IsNotExist(err) {
		return errors.New("No tests found? (no cover profile generated)")
	} else if err != nil {
		return err
	}

	fmt.Printf("\n") // newline between "go test" output and ours
	return parseProfile(profilePath)
}

func parseProfile(fileName string) error {
	profiles, err := cover.ParseProfiles(fileName)
	if err != nil {
		return err
	}

	prof, err := discover.ParseProfile(profiles)
	if err != nil {
		return err
	}

	for _, f := range prof.Files {
		prof.Trim(f)

		// If we filtered out all decls, don't print at all
		if len(f.Decls) == 0 {
			continue
		}

		fn := filepath.Base(prof.Fset.File(f.Pos()).Name())
		fmt.Printf("%s:\n%s\n", fn, strings.Repeat("=", len(fn)))
		printer.Fprint(os.Stdout, prof.Fset, f)
		fmt.Printf("\n\n")
	}
	return nil
}
