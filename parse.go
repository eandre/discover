package discover

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"

	"golang.org/x/tools/cover"
)

// Profile contains a map of statements and funcs that were covered
// by the cover profiles. It supports using the information to trim
// an AST down to the nodes that were actually reached.
type Profile struct {
	Stmts map[ast.Stmt]bool
	Funcs map[*ast.FuncDecl]bool
	Files []*ast.File
	Fset  *token.FileSet
}

// ParseProfile parses a set of coverage profiles to produce a *Profile.
func ParseProfile(profs []*cover.Profile) (*Profile, error) {
	profile := &Profile{
		Stmts: make(map[ast.Stmt]bool),
		Funcs: make(map[*ast.FuncDecl]bool),
		Fset:  token.NewFileSet(),
	}

	for _, prof := range profs {
		file, err := findFile(prof.FileName)
		if err != nil {
			return nil, err
		}

		f, funcs, stmts, err := findFuncs(profile.Fset, file)
		if err != nil {
			return nil, err
		}
		profile.Files = append(profile.Files, f)

		blocks := prof.Blocks
		for len(funcs) > 0 {
			f := funcs[0]
			for i, b := range blocks {
				if b.StartLine > f.endLine || (b.StartLine == f.endLine && b.StartCol >= f.endCol) {
					// Past the end of the func
					funcs = funcs[1:]
					blocks = blocks[i:]
					break
				}
				if b.EndLine < f.startLine || (b.EndLine == f.startLine && b.EndCol <= f.startCol) {
					// Before the beginning of the func
					continue
				}
				if b.Count > 0 {
					profile.Funcs[f.decl] = true
				}
				funcs = funcs[1:]
				break
			}
		}

		blocks = prof.Blocks // reset to all blocks
		for len(stmts) > 0 {
			s := stmts[0]
			for i, b := range blocks {
				if b.StartLine > s.endLine || (b.StartLine == s.endLine && b.StartCol >= s.endCol) {
					// Past the end of the statement
					stmts = stmts[1:]
					blocks = blocks[i:]
					break
				}
				if b.EndLine < s.startLine || (b.EndLine == s.startLine && b.EndCol <= s.startCol) {
					// Before the beginning of the statement
					continue
				}
				if b.Count > 0 {
					profile.Stmts[s.stmt] = true
				}
				stmts = stmts[1:]
				break
			}
		}
	}

	return profile, nil
}

// findFile tries to find the full path to a file, by looking in $GOROOT
// and $GOPATH.
func findFile(file string) (filename string, err error) {
	dir, file := filepath.Split(file)
	if dir != "" {
		dir = dir[:len(dir)-1] // drop trailing '/'
	}
	pkg, err := build.Import(dir, ".", build.FindOnly)
	if err != nil {
		return "", fmt.Errorf("can't find %q: %v", file, err)
	}
	return filepath.Join(pkg.Dir, file), nil
}

// findFuncs parses the file and returns a slice of FuncExtent descriptors.
func findFuncs(fset *token.FileSet, name string) (*ast.File, []*funcExtent, []*stmtExtent, error) {
	parsedFile, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
	if err != nil {
		return nil, nil, nil, err
	}
	visitor := &funcVisitor{fset: fset}
	ast.Walk(visitor, parsedFile)
	return parsedFile, visitor.funcs, visitor.stmts, nil
}

// funcExtent describes a function's extent in the source by file and position.
type funcExtent struct {
	decl      *ast.FuncDecl
	name      string
	startLine int
	startCol  int
	endLine   int
	endCol    int
}

// stmtExtent describes a statement's extent in the source by file and position.
type stmtExtent struct {
	stmt      ast.Stmt
	startLine int
	startCol  int
	endLine   int
	endCol    int
}

// funcVisitor implements the visitor that builds the function position list for a file.
type funcVisitor struct {
	fset  *token.FileSet
	funcs []*funcExtent
	stmts []*stmtExtent
}

// Visit implements the ast.Visitor interface.
func (v *funcVisitor) Visit(node ast.Node) ast.Visitor {
	if f, ok := node.(*ast.FuncDecl); ok {
		start := v.fset.Position(f.Pos())
		end := v.fset.Position(f.End())
		fe := &funcExtent{
			decl:      f,
			startLine: start.Line,
			startCol:  start.Column,
			endLine:   end.Line,
			endCol:    end.Column,
		}
		v.funcs = append(v.funcs, fe)
	} else if s, ok := node.(ast.Stmt); ok {
		start, end := v.fset.Position(s.Pos()), v.fset.Position(s.End())
		se := &stmtExtent{
			stmt:      s,
			startLine: start.Line,
			startCol:  start.Column,
			endLine:   end.Line,
			endCol:    end.Column,
		}
		v.stmts = append(v.stmts, se)
	}
	return v
}
