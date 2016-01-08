package build

import (
	"bytes"
	"go/ast"
	"go/token"
	"strings"
)

// trimComments drops all but the //go: comments, some of which are semantically important.
// We drop all others because they can appear in places that cause our counters
// to appear in syntactically incorrect places. //go: appears at the beginning of
// the line and is syntactically safe.
func trimComments(fset *token.FileSet, file *ast.File) []*ast.CommentGroup {
	var comments []*ast.CommentGroup
	for _, group := range file.Comments {
		var list []*ast.Comment
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, "//go:") && fset.Position(comment.Slash).Column == 1 {
				list = append(list, comment)
			}
		}
		if list != nil {
			comments = append(comments, &ast.CommentGroup{list})
		}
	}
	return comments
}

var slashslash = []byte("//")

// initialComments returns the prefix of content containing only
// whitespace and line comments.  Any +build directives must appear
// within this region.  This approach is more reliable than using
// go/printer to print a modified AST containing comments.
func initialComments(content []byte) []byte {
	// Derived from go/build.Context.shouldBuild.
	end := 0
	p := content
	for len(p) > 0 {
		line := p
		if i := bytes.IndexByte(line, '\n'); i >= 0 {
			line, p = line[:i], p[i+1:]
		} else {
			p = p[len(p):]
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 { // Blank line.
			end = len(content) - len(p)
			continue
		}
		if !bytes.HasPrefix(line, slashslash) { // Not comment line.
			break
		}
	}
	return content[:end]
}
