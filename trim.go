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

	// Node types containing lists of statements
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

	case *ast.ForStmt:
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
		vIf := v.visited(stmt.Body)
		vElse := v.visited(stmt.Else)

		if !vIf {
			var result []ast.Stmt
			// If we didn't reach the body, pull out any calls from
			// init and cond.
			nodes := []*ast.CallExpr{
				v.findCall(stmt.Init),
				v.findCall(stmt.Cond),
			}
			for _, call := range nodes {
				if call != nil {
					result = append(result, &ast.ExprStmt{call})
				}
			}

			if vElse {
				// We reached the else; add it
				result = append(result, v.replaceStmt(stmt.Else)...)
			}
			return result
		} else {
			// We did take the if body
			if !vElse {
				// But not the else: remove it
				stmt.Else = nil
			}

			return []ast.Stmt{stmt}
		}

	case *ast.SelectStmt:
		var list []ast.Stmt
		for _, stmt := range stmt.Body.List {
			if v.visited(stmt) {
				list = append(list, stmt)
			}
		}
		stmt.Body.List = list
		return []ast.Stmt{stmt}
	}
}

// visited is a helper function to return whether or not a statement
// was visited. If stmt is nil, visited returns false.
func (v *trimVisitor) visited(stmt ast.Stmt) bool {
	if stmt == nil { // for convenience with e.g. IfStmt.Else
		return false
	}
	return v.p.Stmts[stmt]
}

// findCall returns the first *ast.CallExpr encountered within the tree
// rooted at node, or nil if no CallExpr was found. This is useful for
// "pulling out" calls out of a statement or expression.
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
