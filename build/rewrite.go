package build

import (
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"golang.org/x/tools/go/loader"
)

const traceIDName = "_discover_trace_id_"

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
	for _, file := range pkg.Files {
		go func(f *ast.File) {
			path := ""
			if dst != "" {
				fn := filepath.Base(prog.Fset.File(f.Pos()).Name())
				path = filepath.Join(dst, fn)
			}
			errs <- rewriteFile(prog.Fset, f, path)
		}(file)
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

func rewriteFile(fset *token.FileSet, file *ast.File, dst string) error {
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
	pkgName := addImport(file, "github.com/eandre/discover/runtimeutil", "_discover_runtimeutil_", "TraceID")
	r := &rewriter{
		fset:  fset,
		file:  file,
		ruPkg: pkgName,
	}
	ast.Walk(r, file)

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

	// TODO add declarations etc?
	return nil
}

type rewriter struct {
	fset  *token.FileSet
	file  *ast.File
	ruPkg string // name of runtimeutil package
}

// addImport adds an import for the specified path, if one does not already
// exist, and returns the local package name.
func addImport(file *ast.File, path, defaultName, defaultVar string) string {
	// Does the package already import it?
	for _, s := range file.Imports {
		if unquote(s.Path.Value) == path {
			if s.Name != nil {
				return s.Name.Name
			}
			return filepath.Base(path)
		}
	}
	newImport := &ast.ImportSpec{
		Name: ast.NewIdent(defaultName),
		Path: &ast.BasicLit{
			Kind:  token.STRING,
			Value: fmt.Sprintf("%q", path),
		},
	}
	impDecl := &ast.GenDecl{
		Tok: token.IMPORT,
		Specs: []ast.Spec{
			newImport,
		},
	}
	// Make the new import the first Decl in the file.
	file.Decls = append(file.Decls, nil)
	copy(file.Decls[1:], file.Decls[0:])
	file.Decls[0] = impDecl
	file.Imports = append(file.Imports, newImport)

	// Now refer to the package, just in case it ends up unused.
	// That is, append to the end of the file the declaration
	//	var _ = <name>.Dummy
	reference := &ast.GenDecl{
		Tok: token.VAR,
		Specs: []ast.Spec{
			&ast.ValueSpec{
				Names: []*ast.Ident{
					ast.NewIdent("_"),
				},
				Values: []ast.Expr{
					&ast.SelectorExpr{
						X:   ast.NewIdent(defaultName),
						Sel: ast.NewIdent(defaultVar),
					},
				},
			},
		},
	}
	file.Decls = append(file.Decls, reference)
	return defaultName
}

// unquote returns the unquoted string.
func unquote(s string) string {
	t, err := strconv.Unquote(s)
	if err != nil {
		log.Fatalf("discover: improperly quoted string %q\n", s)
	}
	return t
}

func (r *rewriter) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case *ast.FuncLit:
		n.Body.List = r.addIDLookup(n.Body.List)
	case *ast.FuncDecl:
		n.Body.List = r.addIDLookup(n.Body.List)
	case *ast.BlockStmt:
		// If it's a switch or select, the body is a list of case clauses; don't tag the block itself.
		if len(n.List) > 0 {
			switch n.List[0].(type) {
			case *ast.CaseClause: // switch
				for _, n := range n.List {
					clause := n.(*ast.CaseClause)
					clause.Body = r.addCounters(clause.Pos(), clause.End(), clause.Body, false)
				}
				return r
			case *ast.CommClause: // select
				for _, n := range n.List {
					clause := n.(*ast.CommClause)
					clause.Body = r.addCounters(clause.Pos(), clause.End(), clause.Body, false)
				}
				return r
			}
		}
		n.List = r.addCounters(n.Lbrace, n.Rbrace+1, n.List, true) // +1 to step past closing brace.
	case *ast.IfStmt:
		ast.Walk(r, n.Body)
		if n.Else == nil {
			return nil
		}
		// The elses are special, because if we have
		//	if x {
		//	} else if y {
		//	}
		// we want to cover the "if y". To do this, we need a place to drop the counter,
		// so we add a hidden block:
		//	if x {
		//	} else {
		//		if y {
		//		}
		//	}
		switch stmt := n.Else.(type) {
		case *ast.IfStmt:
			block := &ast.BlockStmt{
				Lbrace: n.Body.End(), // Start at end of the "if" block so the covered part looks like it starts at the "else".
				List:   []ast.Stmt{stmt},
				Rbrace: stmt.End(),
			}
			n.Else = block
		case *ast.BlockStmt:
			stmt.Lbrace = n.Body.End() // Start at end of the "if" block so the covered part looks like it starts at the "else".
		default:
			panic("unexpected node type in if")
		}
		ast.Walk(r, n.Else)
		return nil
	case *ast.SelectStmt:
		// Don't annotate an empty select - creates a syntax error.
		if n.Body == nil || len(n.Body.List) == 0 {
			return nil
		}
	case *ast.SwitchStmt:
		// Don't annotate an empty switch - creates a syntax error.
		if n.Body == nil || len(n.Body.List) == 0 {
			return nil
		}
	case *ast.TypeSwitchStmt:
		// Don't annotate an empty type switch - creates a syntax error.
		if n.Body == nil || len(n.Body.List) == 0 {
			return nil
		}
	case *ast.GoStmt:
		// Don't walk the generated call because we don't want to
		// re-assign the trace ID at the start of the function literal
		// unlike we normally do. For that reason call ast.Walk() now,
		// and return nil (equivalent, except skips our new func literal).
		ast.Walk(r, n.Call)
		n.Call = r.addGoFunc(n.Call)
		return nil
	}
	return r
}

// addCounters takes a list of statements and adds counters to the beginning of
// each basic block at the top level of that list. For instance, given
//
//	S1
//	if cond {
//		S2
// 	}
//	S3
//
// counters will be added before S1 and before S3. The block containing S2
// will be visited in a separate call.
// TODO: Nested simple blocks get unnecessary (but correct) counters
func (r *rewriter) addCounters(pos, blockEnd token.Pos, list []ast.Stmt, extendToClosingBrace bool) []ast.Stmt {
	// TODO
	return list
}

func (r *rewriter) addIDLookup(list []ast.Stmt) []ast.Stmt {
	// Don't add it to an empty body since there is no need
	if len(list) == 0 {
		return list
	}

	// <traceIDName> := runtimeutil.TraceID()
	assign := &ast.AssignStmt{
		Tok: token.DEFINE,
		Lhs: []ast.Expr{ast.NewIdent(traceIDName)},
		Rhs: []ast.Expr{&ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   ast.NewIdent(r.ruPkg),
				Sel: ast.NewIdent("TraceID"),
			},
		}},
	}

	// _ = <traceIDName> (to prevent errors due to lack of use)
	use := &ast.AssignStmt{
		Tok: token.ASSIGN,
		Lhs: []ast.Expr{ast.NewIdent("_")},
		Rhs: []ast.Expr{ast.NewIdent(traceIDName)},
	}
	// prepend assign and use to list
	list = append([]ast.Stmt{assign, use}, list...)
	return list
}

func (r *rewriter) addGoFunc(call *ast.CallExpr) *ast.CallExpr {
	// Create a func lit to enable tracing for our new goroutine.
	// Unfortunately, the naive solution isn't quite transparent:
	//
	// In "go f(foo, bar, baz)", the arguments are evaluated
	// immediately. In "go func() { f(foo, bar, baz) }()" they are not.
	//
	// To keep this code working, we create a closure at runtime
	// by calling runtimeutil.MakeFunc(foo, bar, baz) and pass that to
	// the created function literal. This ensures that foo, bar, and baz
	// are evaluated immediately. In the case that the last parameter is
	// variadic, runtimeutil.MakeVariadicFunc is called instead.

	fn := "MakeFunc"
	if call.Ellipsis != token.NoPos {
		fn = "MakeVariadicFunc"
	}

	// Make[Variadic]Func(<call.Fun>, args...)
	closureArgs := make([]ast.Expr, 1, len(call.Args)+1)
	closureArgs[0] = call.Fun
	for _, arg := range call.Args {
		closureArgs = append(closureArgs, arg)
	}
	closureCall := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   ast.NewIdent(r.ruPkg),
			Sel: ast.NewIdent(fn),
		},
		Args: closureArgs,
	}

	// function literal that first calls runtimeutil.ChildEnable and then f()
	litParams := &ast.FieldList{List: []*ast.Field{&ast.Field{
		Names: []*ast.Ident{ast.NewIdent("f")},
		Type:  &ast.FuncType{Params: &ast.FieldList{}},
	}}}
	lit := &ast.FuncLit{
		Type: &ast.FuncType{
			Params: litParams,
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{
			// runtimeutil.ChildEnable(<traceIDName>)
			&ast.ExprStmt{X: &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X:   ast.NewIdent(r.ruPkg),
					Sel: ast.NewIdent("ChildEnable"),
				},
				Args: []ast.Expr{ast.NewIdent(traceIDName)},
			}},

			&ast.ExprStmt{X: &ast.CallExpr{
				Fun: ast.NewIdent("f"),
			}},
		}},
	}

	// funcLit(runtime.Make[Variadic]Func(...))
	return &ast.CallExpr{
		Fun:  lit,
		Args: []ast.Expr{closureCall},
	}
}
