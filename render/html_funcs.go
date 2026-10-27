// Copyright 2024 Gin Core Team. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

package render

import (
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	stdparse "text/template/parse"

	gfs "github.com/gin-gonic/gin/internal/fs"
)

// htmlTemplateSource is one template file read from disk or from an
// http.FileSystem: Name is the template name (the file base name) and Text is
// its raw content.
type htmlTemplateSource struct {
	Name string
	Text string
}

// parseTolerantHTML parses sources without requiring every referenced
// template function to already exist. Actions may name funcs that are supplied
// later (for example per request); they are resolved at execution time, where a
// missing func produces an execution error instead of a parse error.
//
// funcs that ARE known (the engine baseline FuncMap) are registered before
// the templates are added, so contextual escaping is computed with their real
// return types. Unknown funcs are treated conservatively, exactly as pipelines
// of dynamic data are, so context-aware escaping (HTML, URL, JS, ...) stays
// intact; typed results such as template.HTML or template.URL still bypass the
// matching escaper at execution.
func parseTolerantHTML(delims Delims, funcs template.FuncMap, sources []htmlTemplateSource) (*template.Template, error) {
	treeSet := make(map[string]*stdparse.Tree)
	for _, source := range sources {
		if _, exists := treeSet[source.Name]; exists {
			return nil, fmt.Errorf("template: multiple templates named %q", source.Name)
		}
		tr := stdparse.New(source.Name)
		tr.Mode = stdparse.SkipFuncCheck
		// Parse registers the main tree (under the file base name) and every
		// {{define}} tree into the shared set, erroring on redefinitions.
		if _, err := tr.Parse(source.Text, delims.Left, delims.Right, treeSet); err != nil {
			return nil, err
		}
	}

	templ := template.New("").Delims(delims.Left, delims.Right).Funcs(funcs)
	// Add the trees in source order. The associated template namespace is the
	// same regardless of insertion order.
	for _, source := range sources {
		var err error
		templ, err = templ.AddParseTree(source.Name, treeSet[source.Name])
		if err != nil {
			return nil, err
		}
	}
	return templ, nil
}

// readHTMLSourcesOS loads sources from local files. When glob is non-empty it
// is expanded like text/template.ParseGlob; otherwise the given files are read.
func readHTMLSourcesOS(glob string, files []string) ([]htmlTemplateSource, error) {
	filenames := files
	if glob != "" {
		matches, err := filepath.Glob(glob)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, &fs.PathError{Op: "parse", Path: glob, Err: fs.ErrNotExist}
		}
		filenames = matches
	}
	sources := make([]htmlTemplateSource, 0, len(filenames))
	for _, filename := range filenames {
		text, err := os.ReadFile(filename)
		if err != nil {
			return nil, err
		}
		sources = append(sources, htmlTemplateSource{Name: filepath.Base(filename), Text: string(text)})
	}
	return sources, nil
}

// readHTMLSourcesFS loads sources from an http.FileSystem, expanding the
// patterns the same way text/template.ParseFS does.
func readHTMLSourcesFS(fileSystem http.FileSystem, patterns []string) ([]htmlTemplateSource, error) {
	fsys := gfs.FileSystem{FileSystem: fileSystem}
	var filenames []string
	for _, pattern := range patterns {
		matches, err := fs.Glob(fsys, pattern)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, &fs.PathError{Op: "parse", Path: pattern, Err: fs.ErrNotExist}
		}
		filenames = append(filenames, matches...)
	}
	sources := make([]htmlTemplateSource, 0, len(filenames))
	for _, filename := range filenames {
		text, err := fs.ReadFile(fsys, filename)
		if err != nil {
			return nil, err
		}
		sources = append(sources, htmlTemplateSource{Name: path.Base(filename), Text: string(text)})
	}
	return sources, nil
}
