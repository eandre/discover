package main

import (
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/eandre/discover"
	"golang.org/x/tools/cover"
)

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: \n\ndiscover [flags] command [<args>...]

The commands are:

	discover [-output=<dir>] test [<testRegexp>]
		Runs "go test -run <testRegexp>" to output a cover profile,
		and then parses it and outputs the result.

	discover [-output=<dir>] parse <cover profile>
		Parses the given cover profile and outputs the result.

For both commands, the output flag specifies a directory to write files to,
as opposed to printing to stdout. If any of the files exist already, they will
be overwritten.
`)
}

var output = flag.String("output", "", "Directory to write output files to (will overwrite existing files)")

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
		importPath := prof.ImportPaths[f]
		if importPath == "" {
			return fmt.Errorf("No import path found for %q", fn)
		}

		if err := outputFile(importPath, fn, prof.Fset, f); err != nil {
			return err
		}
	}
	return nil
}

func outputFile(importPath, name string, fset *token.FileSet, file *ast.File) error {
	if *output != "" {
		// Write to file
		dir := filepath.Join(*output, importPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		target := filepath.Join(dir, name)
		f, err := os.Create(target)
		if err != nil {
			return err
		}
		if err := format.Node(f, fset, file); err != nil {
			return err
		}
		return nil
	}

	// Print to stdout
	fmt.Printf("%s:\n%s\n", name, strings.Repeat("=", len(name)))
	format.Node(os.Stdout, fset, file)
	fmt.Printf("\n\n")
	return nil
}
