package jsonschema

import (
	"fmt"
	"io/fs"
	gopath "path"
	"path/filepath"
	"reflect"
	"strings"

	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
)

type commentOptions struct {
	fullObjectText bool // use the first sentence only?
}

// CommentOption allows for special configuration options when preparing Go
// source files for comment extraction.
type CommentOption func(*commentOptions)

// WithFullComment will configure the comment extraction to process to use an
// object type's full comment text instead of just the synopsis.
func WithFullComment() CommentOption {
	return func(o *commentOptions) {
		o.fullObjectText = true
	}
}

// AddGoComments will update the reflectors comment map with all the comments
// found in the provided source directories including sub-directories, in order to
// generate a dictionary of comments associated with Types and Fields. The results
// will be added to the `Reflect.CommentMap` ready to use with Schema "description"
// fields.
//
// The `go/parser` library is used to extract all the comments and unfortunately doesn't
// have a built-in way to determine the fully qualified name of a package. The `base`
// parameter, the URL used to import that package, is thus required to be able to match
// reflected types.
//
// When parsing type comments, by default we use the `go/doc`'s Synopsis method to extract
// the first phrase only. Field comments, which tend to be much shorter, will include everything.
// This behavior can be changed by using the `WithFullComment` option.
func (r *Reflector) AddGoComments(base, path string, opts ...CommentOption) error {
	if r.CommentMap == nil {
		r.CommentMap = make(map[string]string)
	}
	co := new(commentOptions)
	for _, opt := range opts {
		opt(co)
	}

	return r.extractGoComments(base, path, r.CommentMap, co)
}

// pkgFilesKey groups parsed files by directory and package name so that a
// single directory containing multiple packages (e.g. `foo` and `foo_test`)
// is handled correctly.
type pkgFilesKey struct{ dir, name string }

// typeComment holds a raw type-level doc comment that still needs
// the synopsis trim applied (after doc.NewFromFiles has consumed the ASTs).
type typeComment struct{ key, text string }

func (r *Reflector) extractGoComments(base, path string, commentMap map[string]string, opts *commentOptions) error {
	fset := token.NewFileSet()
	grouped, err := parseGoFiles(fset, path)
	if err != nil {
		return err
	}

	for key, files := range grouped {
		// Normalize to forward slashes so the resulting key matches
		// reflect.Type.PkgPath() on Windows, where filepath.Dir returns backslashes.
		pkg := gopath.Join(base, filepath.ToSlash(key.dir))

		var typeComments []typeComment
		for _, f := range files {
			typeComments = inspectFile(f, pkg, commentMap, typeComments)
		}

		// doc.Package.Synopsis is the non-deprecated replacement for doc.Synopsis;
		// it handles doc links in comment text correctly. doc.NewFromFiles mutates
		// the passed ASTs (nil-ing Doc fields), so it MUST run after the inspection above.
		synopsis := func(s string) string { return s }
		if !opts.fullObjectText {
			if docPkg, derr := doc.NewFromFiles(fset, files, pkg); derr == nil {
				synopsis = docPkg.Synopsis
			}
		}
		for _, tc := range typeComments {
			commentMap[tc.key] = strings.TrimSpace(synopsis(tc.text))
		}
	}

	return nil
}

func parseGoFiles(fset *token.FileSet, path string) (map[pkgFilesKey][]*ast.File, error) {
	grouped := make(map[pkgFilesKey][]*ast.File)
	err := filepath.Walk(path, func(p string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		file, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		k := pkgFilesKey{dir: filepath.Dir(p), name: file.Name.Name}
		grouped[k] = append(grouped[k], file)
		return nil
	})
	return grouped, err
}

// inspectFile walks f, writing field comments directly into commentMap and
// appending type-level comments (which still need synopsis trimming) to typeComments.
func inspectFile(f *ast.File, pkg string, commentMap map[string]string, typeComments []typeComment) []typeComment {
	gtxt := ""
	typ := ""
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.TypeSpec:
			typ = x.Name.String()
			if !ast.IsExported(typ) {
				typ = ""
				return true
			}
			txt := x.Doc.Text()
			if txt == "" && gtxt != "" {
				txt = gtxt
				gtxt = ""
			}
			typeComments = append(typeComments, typeComment{
				key:  fmt.Sprintf("%s.%s", pkg, typ),
				text: txt,
			})
		case *ast.Field:
			writeFieldComment(x, pkg, typ, commentMap)
		case *ast.GenDecl:
			// remember for the next type
			gtxt = x.Doc.Text()
		}
		return true
	})
	return typeComments
}

func writeFieldComment(x *ast.Field, pkg, typ string, commentMap map[string]string) {
	if typ == "" {
		return
	}
	txt := x.Doc.Text()
	if txt == "" {
		txt = x.Comment.Text()
	}
	if txt == "" {
		return
	}
	for _, n := range x.Names {
		if ast.IsExported(n.String()) {
			commentMap[fmt.Sprintf("%s.%s.%s", pkg, typ, n)] = strings.TrimSpace(txt)
		}
	}
}

func (r *Reflector) lookupComment(t reflect.Type, name string) string {
	if r.LookupComment != nil {
		if comment := r.LookupComment(t, name); comment != "" {
			return comment
		}
	}

	if r.CommentMap == nil {
		return ""
	}

	n := fullyQualifiedTypeName(t)
	if name != "" {
		n = n + "." + name
	}

	return r.CommentMap[n]
}
