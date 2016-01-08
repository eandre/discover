package build

import (
	"go/ast"
	"go/printer"
	"go/token"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/tools/go/loader"
)

func Rewrite(prog *loader.Program, dst string) error {
	pkgs := prog.InitialPackages()
	errs := make(chan error, len(pkgs))
	for _, pkg := range prog.InitialPackages() {
		go func(p *loader.PackageInfo) {
			errs <- rewritePkg(prog, p, dst)
		}(pkg)
	}

	// Wait for rewrites to finish
	var firstErr error
	for i := 0; i < len(pkgs); i++ {
		err := <-errs
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func rewritePkg(prog *loader.Program, pkg *loader.PackageInfo, dst string) error {
	errs := make(chan error, len(pkg.Files))
	for i, file := range pkg.Files {
		go func(idx int, f *ast.File) {
			path := ""
			if dst != "" {
				fn := filepath.Base(prog.Fset.File(f.Pos()).Name())
				path = filepath.Join(dst, fn)
			}

			// Only add declarations once per package
			addDecl := idx == 0
			errs <- rewriteFile(prog.Fset, f, path, addDecl)
		}(i, file)
	}

	// Wait for rewrites to finish
	var firstErr error
	for i := 0; i < len(pkg.Files); i++ {
		err := <-errs
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

var stdoutLock sync.Mutex

func rewriteFile(fset *token.FileSet, file *ast.File, dst string, addDecl bool) error {
	srcPath := fset.Position(file.Pos()).Filename
	src, err := ioutil.ReadFile(srcPath)
	if err != nil {
		return err
	}

	output := os.Stdout
	if dst != "" {
		// Make sure parent dir exists before creating file
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		output, err = os.Create(dst)
		if err != nil {
			return err
		}
	}

	file.Comments = trimComments(fset, file)
	ruPkg := addImport(file, "github.com/eandre/discover/runtimeutil", "_discover_runtimeutil_", "TraceID")
	atomicPkg := addImport(file, "sync/atomic", "_discover_atomic_", "AddUint32")

	// First add cover info
	fc := &fileCover{
		fset:      fset,
		file:      file,
		atomicPkg: atomicPkg,
		name:      filepath.Base(srcPath),
	}
	ast.Walk(fc, file)

	// Then add tracing after
	ft := &fileTracer{
		fset:  fset,
		file:  file,
		ruPkg: ruPkg,
	}
	ast.Walk(ft, file)

	if dst == "" {
		// We're writing to stdout; acquire the lock so we don't
		// clobber ourselves
		stdoutLock.Lock()
		defer stdoutLock.Unlock()
	}

	if _, err := output.Write(initialComments(src)); err != nil {
		return err
	}

	if err := printer.Fprint(output, fset, file); err != nil {
		return err
	}

	if addDecl {
		fc.addVariables(output)
	}
	return nil
}
