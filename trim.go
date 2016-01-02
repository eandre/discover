package discover

import "go/ast"

// Trim trims the AST rooted at node based on the coverage profile,
// removing irrelevant and unreached parts of the program.
// If the node is an *ast.File, comments are updated as well using
// an ast.CommentMap.
func (p *Profile) Trim(node ast.Node) {
	if f, ok := node.(*ast.File); ok {
		cmap := ast.NewCommentMap(p.Fset, f, f.Comments)
		ast.Walk(&trimVisitor{p}, f)
		f.Comments = cmap.Filter(f).Comments()
	} else {
		ast.Walk(&trimVisitor{p}, node)
	}
}

// trimVisitor is an ast.Visitor that trims nodes as it walks the tree.
type trimVisitor struct {
	p *Profile
}

func (v *trimVisitor) Visit(node ast.Node) ast.Visitor {
	var list *[]ast.Stmt
	switch node := node.(type) {
	case *ast.File:
		var replaced []ast.Decl
		for _, decl := range node.Decls {
			// Remove non-func declarations and funcs that were not covered
			if f, ok := decl.(*ast.FuncDecl); ok && v.p.Funcs[f] {
				replaced = append(replaced, decl)
			}
		}
		node.Decls = replaced
	case *ast.BlockStmt:
		list = &node.List
	case *ast.CommClause:
		list = &node.Body
	case *ast.CaseClause:
		list = &node.Body
	}

	if list != nil {
		var replaced []ast.Stmt
		for _, stmt := range *list {
			replaced = append(replaced, v.replaceStmt(stmt)...)
		}

		*list = replaced
	}
	return v
}

// replaceStmt returns the (possibly many) statements that should replace
// stmt. Generally a stmt is untouched or removed, but in some cases a
// single stmt can result in multiple statements. This is usually only the case
// when removing a block that was not taken, but pulling out function calls
// that were part of the initialization of the block.
func (v *trimVisitor) replaceStmt(stmt ast.Stmt) []ast.Stmt {
	switch stmt := stmt.(type) {
	case nil:
		return nil

	default:
		// Keep original
		return []ast.Stmt{stmt}

	case *ast.RangeStmt:
		if v.visited(stmt.Body) {
			return []ast.Stmt{stmt}
		}

		call := v.findCall(stmt.X)
		if call != nil {
			return []ast.Stmt{&ast.ExprStmt{call}}
		}
		return nil

	case *ast.RangeStmt:
		if v.visited(stmt.Body) {
			return []ast.Stmt{stmt}
		}

		nodes := []*ast.CallExpr{
			v.findCall(stmt.Init),
			v.findCall(stmt.Cond),
			v.findCall(stmt.Post),
		}

		var result []ast.Stmt
		for _, call := range nodes {
			if call != nil {
				result = append(result, &ast.ExprStmt{call})
			}
		}
		return result

	case *ast.IfStmt:
		var result []ast.Stmt
		vIf := v.visited(stmt.Body)
		vElse := v.visited(stmt.Else)

		if !vIf && !vElse {
			// If we have a CallExpr in the init or cond, move it out.
			// Don't add the if statement either way since it was
			// not visited.
			initCall := v.findCall(stmt.Init)
			condCall := v.findCall(stmt.Cond)
			if initCall != nil {
				result = append(result, &ast.ExprStmt{initCall})
			}
			if condCall != nil {
				result = append(result, &ast.ExprStmt{condCall})
			}
		} else if !vIf && vElse {
			// Only add 'else'
			result = append(result, v.replaceStmt(stmt.Else)...)
		} else if vIf && !vElse {
			// Remove else
			stmt.Else = nil
			result = append(result, stmt)
		}
		return result
	}
}

func (v *trimVisitor) visited(stmt ast.Stmt) bool {
	if stmt == nil { // for convenience with e.g. IfStmt.Else
		return false
	}
	return v.p.Stmts[stmt]
}

func (v *trimVisitor) findCall(node ast.Node) *ast.CallExpr {
	if node == nil { // for convenience
		return nil
	}

	var call *ast.CallExpr
	ast.Inspect(node, func(n ast.Node) bool {
		if call != nil {
			return false
		}
		c, ok := n.(*ast.CallExpr)
		if ok {
			call = c
			return false
		}
		return true
	})
	return call
}
