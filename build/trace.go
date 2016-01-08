package build

import (
	"go/ast"
	"go/token"
)

const traceIDName = "_discover_trace_id_"

type fileTracer struct {
	fset  *token.FileSet
	file  *ast.File
	ruPkg string // name of runtimeutil package
}

func (f *fileTracer) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case *ast.FuncLit:
		n.Body.List = f.addIDLookup(n.Body.List)
	case *ast.FuncDecl:
		n.Body.List = f.addIDLookup(n.Body.List)
	case *ast.GoStmt:
		// Don't walk the generated call because we don't want to
		// re-assign the trace ID at the start of the function literal
		// unlike we normally do. For that reason call ast.Walk() now,
		// and return nil (equivalent, except skips our new func literal).
		ast.Walk(f, n.Call)
		n.Call = f.addGoFunc(n.Call)
		return nil
	}
	return f
}

func (f *fileTracer) addIDLookup(list []ast.Stmt) []ast.Stmt {
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
				X:   ast.NewIdent(f.ruPkg),
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

func (f *fileTracer) addGoFunc(call *ast.CallExpr) *ast.CallExpr {
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
			X:   ast.NewIdent(f.ruPkg),
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
					X:   ast.NewIdent(f.ruPkg),
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
