// Copyright 2014 Manu Martinez-Almeida. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

package render

import (
	"errors"
	"html/template"
	"net/http"

	gfs "github.com/gin-gonic/gin/internal/fs"
)

// Delims represents a set of Left and Right delimiters for HTML template rendering.
type Delims struct {
	// Left delimiter, defaults to {{.
	Left string
	// Right delimiter, defaults to }}.
	Right string
}

// HTMLRender interface is to be implemented by HTMLProduction and HTMLDebug.
type HTMLRender interface {
	// Instance returns an HTML instance.
	Instance(string, any) Render
}

// HTMLRenderFuncs is an optional HTMLRender capability for renderers able to
// take request-scoped template funcs into account when building an instance.
// funcMap already contains the engine baseline merged with the request
// function stack. The built-in HTMLProduction and HTMLDebug implement it.
type HTMLRenderFuncs interface {
	HTMLRender
	// InstanceWithFuncs behaves like Instance but makes funcMap available to
	// the template before it is executed.
	InstanceWithFuncs(name string, data any, funcMap template.FuncMap) Render
}

// HTMLFuncsRenderer is implemented by HTML Render values that can bind an
// additional, request-scoped template.FuncMap immediately before the
// template is executed. The built-in HTML type implements it, so every HTML
// exit (Context.HTML, Context.HTMLBlob and direct use of the Render interface)
// gets the same request-scoped behavior. Custom HTML renders may implement it
// as well.
type HTMLFuncsRenderer interface {
	// WithFuncs returns an equivalent Render bound to funcMap. funcMap is
	// expected to already contain the engine baseline FuncMap merged with the
	// request function stack frames, nearest first.
	WithFuncs(funcMap template.FuncMap) Render
}

// HTMLProduction contains template reference and its delims.
type HTMLProduction struct {
	Template *template.Template
	Delims   Delims

	// The following fields are optional. When the template was loaded through
	// Engine.LoadHTMLGlob/LoadHTMLFiles/LoadHTMLFS, the original sources are
	// retained here, so request-scoped funcs whose names did not exist when
	// Template was parsed can still be bound by parsing the sources again.
	// When a renderer is created only through Engine.SetHTMLTemplate, all of
	// them are zero values and request-scoped funcs can override funcs that
	// were known at parse time, but cannot introduce brand-new names.
	Files      []string
	Glob       string
	FileSystem http.FileSystem
	Patterns   []string
	FuncMap    template.FuncMap
}

// HTMLDebug contains template delims and pattern and function with file list.
type HTMLDebug struct {
	Files      []string
	Glob       string
	FileSystem http.FileSystem
	Patterns   []string
	Delims     Delims
	FuncMap    template.FuncMap
}

// HTML contains template reference and its name with given interface object.
type HTML struct {
	Template *template.Template
	Name     string
	Data     any

	// funcMap is the engine baseline FuncMap merged with the request-scoped
	// stack top. nil means no request frame was pushed and the template is
	// executed exactly as before.
	funcMap template.FuncMap
	// bound reports Template was already parsed/derived with funcMap, so it
	// must not be bound again.
	bound bool
	// sources retained for a fresh parse when funcMap introduces new names.
	files      []string
	glob       string
	fileSystem http.FileSystem
	patterns   []string
	delims     Delims
}

var htmlContentType = []string{"text/html; charset=utf-8"}

var errHTMLRendererNotConfigured = errors.New("html renderer is not configured")

var errHTMLNoSources = errors.New("html production renderer has no retained template sources")

// Instance (HTMLProduction) returns an HTML instance which it realizes Render interface.
func (r HTMLProduction) Instance(name string, data any) Render {
	return HTML{
		Template:   r.Template,
		Name:       name,
		Data:       data,
		files:      r.Files,
		glob:       r.Glob,
		fileSystem: r.FileSystem,
		patterns:   r.Patterns,
		delims:     r.Delims,
	}
}

// InstanceWithFuncs (HTMLProduction) returns an HTML instance which binds
// funcMap immediately before executing the template.
func (r HTMLProduction) InstanceWithFuncs(name string, data any, funcMap template.FuncMap) Render {
	return HTML{
		Template:   r.Template,
		Name:       name,
		Data:       data,
		funcMap:    funcMap,
		files:      r.Files,
		glob:       r.Glob,
		fileSystem: r.FileSystem,
		patterns:   r.Patterns,
		delims:     r.Delims,
	}
}

// Instance (HTMLDebug) returns an HTML instance which it realizes Render interface.
func (r HTMLDebug) Instance(name string, data any) Render {
	return HTML{
		Template:   r.loadTemplate(r.FuncMap, false),
		Name:       name,
		Data:       data,
		files:      r.Files,
		glob:       r.Glob,
		fileSystem: r.FileSystem,
		patterns:   r.Patterns,
		delims:     r.Delims,
	}
}

// InstanceWithFuncs (HTMLDebug) parses the templates lazily with funcMap, so
// the request sees both overrides and funcs with brand-new names, paying only
// a single parse.
func (r HTMLDebug) InstanceWithFuncs(name string, data any, funcMap template.FuncMap) Render {
	return HTML{
		Template:   r.loadTemplate(funcMap, true),
		Name:       name,
		Data:       data,
		funcMap:    funcMap,
		bound:      true,
		files:      r.Files,
		glob:       r.Glob,
		fileSystem: r.FileSystem,
		patterns:   r.Patterns,
		delims:     r.Delims,
	}
}

// readSources collects the retained template sources in the renderer's
// configured storage.
func (r HTMLDebug) readSources() ([]htmlTemplateSource, error) {
	if r.FileSystem != nil && len(r.Patterns) > 0 {
		return readHTMLSourcesFS(r.FileSystem, r.Patterns)
	}
	return readHTMLSourcesOS(r.Glob, r.Files)
}

func (r HTMLDebug) loadTemplate(funcMap template.FuncMap, tolerant bool) *template.Template {
	if funcMap == nil {
		funcMap = template.FuncMap{}
	}
	if tolerant {
		sources, err := r.readSources()
		if err != nil {
			panic(err)
		}
		return template.Must(parseTolerantHTML(r.Delims, funcMap, sources))
	}
	if len(r.Files) > 0 {
		return template.Must(template.New("").Delims(r.Delims.Left, r.Delims.Right).Funcs(funcMap).ParseFiles(r.Files...))
	}
	if r.Glob != "" {
		return template.Must(template.New("").Delims(r.Delims.Left, r.Delims.Right).Funcs(funcMap).ParseGlob(r.Glob))
	}
	if r.FileSystem != nil && len(r.Patterns) > 0 {
		return template.Must(template.New("").Delims(r.Delims.Left, r.Delims.Right).Funcs(funcMap).ParseFS(
			gfs.FileSystem{FileSystem: r.FileSystem}, r.Patterns...))
	}
	panic("the HTML debug render was created without files or glob pattern or file system with patterns")
}

// WithFuncs returns an HTML render identical to r but which applies funcMap
// to the template before executing it. The engine-wide template is never
// mutated. An already bound render is returned unchanged.
func (r HTML) WithFuncs(funcMap template.FuncMap) Render {
	if r.bound {
		return r
	}
	r.funcMap = funcMap
	return r
}

// hasSources reports whether the original template sources were retained.
func (r HTML) hasSources() bool {
	return r.glob != "" || len(r.files) > 0 || (r.fileSystem != nil && len(r.patterns) > 0)
}

// readSources collects the retained template sources carried by the instance.
func (r HTML) readSources() ([]htmlTemplateSource, error) {
	if r.fileSystem != nil && len(r.patterns) > 0 {
		return readHTMLSourcesFS(r.fileSystem, r.patterns)
	}
	return readHTMLSourcesOS(r.glob, r.files)
}

// bindFuncs returns a template ready to execute with the request-scoped
// funcMap. When the original sources are available they are parsed tolerantly
// with funcMap (both new func names and overrides bind; a func still missing
// errors at execution, not at parse). Otherwise the parsed template is cloned
// and the funcs are applied to the clone, which supports overriding funcs that
// were known at parse time and never mutates the engine-wide template.
func (r HTML) bindFuncs() (*template.Template, error) {
	if r.hasSources() {
		sources, err := r.readSources()
		if err != nil {
			return nil, err
		}
		return parseTolerantHTML(r.delims, r.funcMap, sources)
	}
	templ, err := r.Template.Clone()
	if err != nil {
		return nil, err
	}
	return templ.Funcs(r.funcMap), nil
}

// ParseSources parses the sources retained on the production renderer. The
// templates are first parsed strictly, preserving the exact eager validation
// of ParseFiles/ParseGlob/ParseFS. If strict parsing fails only because the
// template text references funcs that are not part of FuncMap yet (they may be
// pushed per request), parsing is retried tolerantly: those funcs are then
// resolved at execution time. Hard errors (syntax, IO, ...) still make the
// strict error surface.
func (r HTMLProduction) ParseSources() (*template.Template, error) {
	if r.Glob == "" && len(r.Files) == 0 && !(r.FileSystem != nil && len(r.Patterns) > 0) {
		return nil, errHTMLNoSources
	}
	strict, strictErr := r.parseStrict()
	if strictErr == nil {
		return strict, nil
	}
	var sources []htmlTemplateSource
	var err error
	if r.FileSystem != nil && len(r.Patterns) > 0 {
		sources, err = readHTMLSourcesFS(r.FileSystem, r.Patterns)
	} else {
		sources, err = readHTMLSourcesOS(r.Glob, r.Files)
	}
	if err != nil {
		return nil, strictErr
	}
	tolerant, err := parseTolerantHTML(r.Delims, r.FuncMap, sources)
	if err != nil {
		return nil, strictErr
	}
	return tolerant, nil
}

func (r HTMLProduction) parseStrict() (*template.Template, error) {
	templ := template.New("").Delims(r.Delims.Left, r.Delims.Right).Funcs(r.FuncMap)
	switch {
	case len(r.Files) > 0:
		return templ.ParseFiles(r.Files...)
	case r.Glob != "":
		return templ.ParseGlob(r.Glob)
	case r.FileSystem != nil && len(r.Patterns) > 0:
		return templ.ParseFS(gfs.FileSystem{FileSystem: r.FileSystem}, r.Patterns...)
	default:
		return nil, errHTMLNoSources
	}
}

// Render (HTML) executes template and writes its result with custom ContentType for response.
func (r HTML) Render(w http.ResponseWriter) error {
	r.WriteContentType(w)
	if r.Template == nil {
		return errHTMLRendererNotConfigured
	}

	templ := r.Template
	if !r.bound && len(r.funcMap) > 0 {
		boundTemplate, err := r.bindFuncs()
		if err != nil {
			return err
		}
		templ = boundTemplate
	}

	if r.Name == "" {
		return templ.Execute(w, r.Data)
	}
	return templ.ExecuteTemplate(w, r.Name, r.Data)
}

// WriteContentType (HTML) writes HTML ContentType.
func (r HTML) WriteContentType(w http.ResponseWriter) {
	writeContentType(w, htmlContentType)
}
