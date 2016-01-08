package build

import (
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

var CoverVar = "DiscoverCover"

// Block represents the information about a basic block to be recorded in the analysis.
// Note: Our definition of basic block is based on control structures; we don't break
// apart && and ||. We could but it doesn't seem important enough to bothef.
type Block struct {
	startByte token.Pos
	endByte   token.Pos
	numStmt   int
}

type fileCover struct {
	fset      *token.FileSet
	file      *ast.File
	blocks    []Block
	atomicPkg string
	name      string
}

func (f *fileCover) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case *ast.BlockStmt:
		// If it's a switch or select, the body is a list of case clauses; don't tag the block itself.
		if len(n.List) > 0 {
			switch n.List[0].(type) {
			case *ast.CaseClause: // switch
				for _, n := range n.List {
					clause := n.(*ast.CaseClause)
					clause.Body = f.addCounters(clause.Pos(), clause.End(), clause.Body, false)
				}
				return f
			case *ast.CommClause: // select
				for _, n := range n.List {
					clause := n.(*ast.CommClause)
					clause.Body = f.addCounters(clause.Pos(), clause.End(), clause.Body, false)
				}
				return f
			}
		}
		n.List = f.addCounters(n.Lbrace, n.Rbrace+1, n.List, true) // +1 to step past closing brace.
	case *ast.IfStmt:
		ast.Walk(f, n.Body)
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
		ast.Walk(f, n.Else)
		return nil
	case *ast.SelectStmt:
		// Don't annotate an empty select - creates a syntax errof.
		if n.Body == nil || len(n.Body.List) == 0 {
			return nil
		}
	case *ast.SwitchStmt:
		// Don't annotate an empty switch - creates a syntax errof.
		if n.Body == nil || len(n.Body.List) == 0 {
			return nil
		}
	case *ast.TypeSwitchStmt:
		// Don't annotate an empty type switch - creates a syntax errof.
		if n.Body == nil || len(n.Body.List) == 0 {
			return nil
		}
	}
	return f
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
func (f *fileCover) addCounters(pos, blockEnd token.Pos, list []ast.Stmt, extendToClosingBrace bool) []ast.Stmt {
	// Special case: make sure we add a counter to an empty block. Can't do this below
	// or we will add a counter to an empty statement list after, say, a return statement.
	if len(list) == 0 {
		return []ast.Stmt{f.newCounter(pos, blockEnd, 0)}
	}
	// We have a block (statement list), but it may have several basic blocks due to the
	// appearance of statements that affect the flow of control.
	var newList []ast.Stmt
	for {
		// Find first statement that affects flow of control (break, continue, if, etc.).
		// It will be the last statement of this basic block.
		var last int
		end := blockEnd
		for last = 0; last < len(list); last++ {
			end = f.statementBoundary(list[last])
			if f.endsBasicSourceBlock(list[last]) {
				extendToClosingBrace = false // Block is broken up now.
				last++
				break
			}
		}
		if extendToClosingBrace {
			end = blockEnd
		}
		if pos != end { // Can have no source to cover if e.g. blocks abut.
			newList = append(newList, f.newCounter(pos, end, last))
		}
		newList = append(newList, list[0:last]...)
		list = list[last:]
		if len(list) == 0 {
			break
		}
		pos = list[0].Pos()
	}
	return newList
}

// intLiteral returns an ast.BasicLit representing the integer value.
func (f *fileCover) intLiteral(i int) *ast.BasicLit {
	node := &ast.BasicLit{
		Kind:  token.INT,
		Value: fmt.Sprint(i),
	}
	return node
}

// index returns an ast.BasicLit representing the number of counters present.
func (f *fileCover) index() *ast.BasicLit {
	return f.intLiteral(len(f.blocks))
}

// atomicCounterStmt returns the expression: atomic.AddUint32(&__count[23], 1)
func atomicCounterStmt(f *fileCover, counter ast.Expr) ast.Stmt {
	return &ast.ExprStmt{
		X: &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   ast.NewIdent(f.atomicPkg),
				Sel: ast.NewIdent("AddUint32"),
			},
			Args: []ast.Expr{&ast.UnaryExpr{
				Op: token.AND,
				X:  counter,
			},
				f.intLiteral(1),
			},
		},
	}
}

// newCounter creates a new counter expression of the appropriate form.
func (f *fileCover) newCounter(start, end token.Pos, numStmt int) ast.Stmt {
	traceIndex := &ast.IndexExpr{
		X: &ast.SelectorExpr{
			X:   ast.NewIdent(CoverVar),
			Sel: ast.NewIdent("Count"),
		},
		Index: ast.NewIdent(traceIDName),
	}
	counter := &ast.IndexExpr{
		X:     traceIndex,
		Index: f.index(),
	}
	stmt := atomicCounterStmt(f, counter)
	f.blocks = append(f.blocks, Block{start, end, numStmt})
	return stmt
}

// hasFuncLiteral reports the existence and position of the first func literal
// in the node, if any. If a func literal appears, it usually marks the termination
// of a basic block because the function body is itself a block.
// Therefore we draw a line at the start of the body of the first function literal we find.
// TODO: what if there's more than one? Probably doesn't matter much.
func hasFuncLiteral(n ast.Node) (bool, token.Pos) {
	if n == nil {
		return false, 0
	}
	var literal funcLitFinder
	ast.Walk(&literal, n)
	return literal.found(), token.Pos(literal)
}

// statementBoundary finds the location in s that terminates the current basic
// block in the source.
func (f *fileCover) statementBoundary(s ast.Stmt) token.Pos {
	// Control flow statements are easy.
	switch s := s.(type) {
	case *ast.BlockStmt:
		// Treat blocks like basic blocks to avoid overlapping counters.
		return s.Lbrace
	case *ast.IfStmt:
		found, pos := hasFuncLiteral(s.Init)
		if found {
			return pos
		}
		found, pos = hasFuncLiteral(s.Cond)
		if found {
			return pos
		}
		return s.Body.Lbrace
	case *ast.ForStmt:
		found, pos := hasFuncLiteral(s.Init)
		if found {
			return pos
		}
		found, pos = hasFuncLiteral(s.Cond)
		if found {
			return pos
		}
		found, pos = hasFuncLiteral(s.Post)
		if found {
			return pos
		}
		return s.Body.Lbrace
	case *ast.LabeledStmt:
		return f.statementBoundary(s.Stmt)
	case *ast.RangeStmt:
		found, pos := hasFuncLiteral(s.X)
		if found {
			return pos
		}
		return s.Body.Lbrace
	case *ast.SwitchStmt:
		found, pos := hasFuncLiteral(s.Init)
		if found {
			return pos
		}
		found, pos = hasFuncLiteral(s.Tag)
		if found {
			return pos
		}
		return s.Body.Lbrace
	case *ast.SelectStmt:
		return s.Body.Lbrace
	case *ast.TypeSwitchStmt:
		found, pos := hasFuncLiteral(s.Init)
		if found {
			return pos
		}
		return s.Body.Lbrace
	}
	// If not a control flow statement, it is a declaration, expression, call, etc. and it may have a function literal.
	// If it does, that's tricky because we want to exclude the body of the function from this block.
	// Draw a line at the start of the body of the first function literal we find.
	// TODO: what if there's more than one? Probably doesn't matter much.
	found, pos := hasFuncLiteral(s)
	if found {
		return pos
	}
	return s.End()
}

// endsBasicSourceBlock reports whether s changes the flow of control: break, if, etc.,
// or if it's just problematic, for instance contains a function literal, which will complicate
// accounting due to the block-within-an expression.
func (f *fileCover) endsBasicSourceBlock(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.BlockStmt:
		// Treat blocks like basic blocks to avoid overlapping counters.
		return true
	case *ast.BranchStmt:
		return true
	case *ast.ForStmt:
		return true
	case *ast.IfStmt:
		return true
	case *ast.LabeledStmt:
		return f.endsBasicSourceBlock(s.Stmt)
	case *ast.RangeStmt:
		return true
	case *ast.SwitchStmt:
		return true
	case *ast.SelectStmt:
		return true
	case *ast.TypeSwitchStmt:
		return true
	case *ast.ExprStmt:
		// Calls to panic change the flow.
		// We really should verify that "panic" is the predefined function,
		// but without type checking we can't and the likelihood of it being
		// an actual problem is vanishingly small.
		if call, ok := s.X.(*ast.CallExpr); ok {
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "panic" && len(call.Args) == 1 {
				return true
			}
		}
	}
	found, _ := hasFuncLiteral(s)
	return found
}

// funcLitFinder implements the ast.Visitor pattern to find the location of any
// function literal in a subtree.
type funcLitFinder token.Pos

func (f *funcLitFinder) Visit(node ast.Node) (w ast.Visitor) {
	if f.found() {
		return nil // Prune search.
	}
	switch n := node.(type) {
	case *ast.FuncLit:
		*f = funcLitFinder(n.Body.Lbrace)
		return nil // Prune search.
	}
	return f
}

func (f *funcLitFinder) found() bool {
	return token.Pos(*f) != token.NoPos
}

// Sort interface for []block1; used for self-check in addVariables.

type block1 struct {
	Block
	index int
}

type blockSlice []block1

func (b blockSlice) Len() int           { return len(b) }
func (b blockSlice) Less(i, j int) bool { return b[i].startByte < b[j].startByte }
func (b blockSlice) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }

// offset translates a token position into a 0-indexed byte offset.
func (f *fileCover) offset(pos token.Pos) int {
	return f.fset.Position(pos).Offset
}

// addVariables adds to the end of the file the declarations to set up the counter and position variables.
func (f *fileCover) addVariables(w io.Writer) {
	// Self-check: Verify that the instrumented basic blocks are disjoint.
	t := make([]block1, len(f.blocks))
	for i := range f.blocks {
		t[i].Block = f.blocks[i]
		t[i].index = i
	}
	sort.Sort(blockSlice(t))
	for i := 1; i < len(t); i++ {
		if t[i-1].endByte > t[i].startByte {
			fmt.Fprintf(os.Stderr, "cover: internal error: block %d overlaps block %d\n", t[i-1].index, t[i].index)
			// Note: error message is in byte positions, not token positions.
			fmt.Fprintf(os.Stderr, "\t%s:#%d,#%d %s:#%d,#%d\n",
				f.name, f.offset(t[i-1].startByte), f.offset(t[i-1].endByte),
				f.name, f.offset(t[i].startByte), f.offset(t[i].endByte))
		}
	}

	// Declare the coverage struct as a package-level variable.
	fmt.Fprintf(w, "\nvar %s = struct {\n", CoverVar)
	fmt.Fprintf(w, "\tCount     [%d]uint32\n", len(f.blocks))
	fmt.Fprintf(w, "\tPos       [3 * %d]uint32\n", len(f.blocks))
	fmt.Fprintf(w, "\tNumStmt   [%d]uint16\n", len(f.blocks))
	fmt.Fprintf(w, "} {\n")

	// Initialize the position array field.
	fmt.Fprintf(w, "\tPos: [3 * %d]uint32{\n", len(f.blocks))

	// A nice long list of positions. Each position is encoded as follows to reduce size:
	// - 32-bit starting line number
	// - 32-bit ending line number
	// - (16 bit ending column number << 16) | (16-bit starting column number).
	for i, block := range f.blocks {
		start := f.fset.Position(block.startByte)
		end := f.fset.Position(block.endByte)
		fmt.Fprintf(w, "\t\t%d, %d, %#x, // [%d]\n", start.Line, end.Line, (end.Column&0xFFFF)<<16|(start.Column&0xFFFF), i)
	}

	// Close the position array.
	fmt.Fprintf(w, "\t},\n")

	// Initialize the position array field.
	fmt.Fprintf(w, "\tNumStmt: [%d]uint16{\n", len(f.blocks))

	// A nice long list of statements-per-block, so we can give a conventional
	// valuation of "percent covered". To save space, it's a 16-bit number, so we
	// clamp it if it overflows - won't matter in practice.
	for i, block := range f.blocks {
		n := block.numStmt
		if n > 1<<16-1 {
			n = 1<<16 - 1
		}
		fmt.Fprintf(w, "\t\t%d, // %d\n", n, i)
	}

	// Close the statements-per-block array.
	fmt.Fprintf(w, "\t},\n")

	// Close the struct initialization.
	fmt.Fprintf(w, "}\n")
}
