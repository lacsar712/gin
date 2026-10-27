// Copyright 2024 Gin Core Team. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

package render

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTemplateFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(file, []byte(content), 0o644))
	return file
}

func TestHTMLProductionWithFuncsOverride(t *testing.T) {
	w := httptest.NewRecorder()
	templ := template.Must(template.New("t").Funcs(template.FuncMap{
		"who": func() string { return "base" },
	}).Parse(`{{who}}`))

	htmlRender := HTMLProduction{Template: templ}
	instance := htmlRender.InstanceWithFuncs("t", nil, template.FuncMap{
		"who": func() string { return "request" },
	})

	require.NoError(t, instance.Render(w))
	assert.Equal(t, "request", w.Body.String())

	// The engine-wide template is not mutated.
	var buf strings.Builder
	require.NoError(t, templ.Execute(&buf, nil))
	assert.Equal(t, "base", buf.String())
}

func TestHTMLProductionSourcesNewFunc(t *testing.T) {
	file := writeTemplateFile(t, "upper.tmpl", `[{{upper "abc"}}]`)
	production := HTMLProduction{
		Delims:  Delims{"{{", "}}"},
		Glob:    filepath.Join(filepath.Dir(file), "*"),
		FuncMap: template.FuncMap{},
	}
	templ := template.Must(production.ParseSources())
	production.Template = templ

	// Without request funcs the unknown func errors at execution.
	w1 := httptest.NewRecorder()
	require.Error(t, production.Instance("upper.tmpl", nil).Render(w1))

	// With request funcs the new name binds from the retained sources.
	w2 := httptest.NewRecorder()
	instance := production.InstanceWithFuncs("upper.tmpl", nil, template.FuncMap{
		"upper": strings.ToUpper,
	})
	require.NoError(t, instance.Render(w2))
	assert.Equal(t, "[ABC]", w2.Body.String())
}

func TestHTMLProductionFilesNewFunc(t *testing.T) {
	file := writeTemplateFile(t, "upper.tmpl", `[{{upper "abc"}}]`)
	production := HTMLProduction{
		Files:   []string{file},
		FuncMap: template.FuncMap{},
	}
	production.Template = template.Must(production.ParseSources())

	w := httptest.NewRecorder()
	instance := production.InstanceWithFuncs("upper.tmpl", nil, template.FuncMap{
		"upper": strings.ToUpper,
	})
	require.NoError(t, instance.Render(w))
	assert.Equal(t, "[ABC]", w.Body.String())
}

func TestHTMLProductionFSNewFunc(t *testing.T) {
	file := writeTemplateFile(t, "upper.tmpl", `[{{upper "abc"}}]`)
	production := HTMLProduction{
		FileSystem: http.Dir(filepath.Dir(file)),
		Patterns:   []string{"upper.tmpl"},
		FuncMap:    template.FuncMap{},
	}
	production.Template = template.Must(production.ParseSources())

	w := httptest.NewRecorder()
	instance := production.InstanceWithFuncs("upper.tmpl", nil, template.FuncMap{
		"upper": strings.ToUpper,
	})
	require.NoError(t, instance.Render(w))
	assert.Equal(t, "[ABC]", w.Body.String())
}

func TestHTMLDebugInstanceWithFuncs(t *testing.T) {
	file := writeTemplateFile(t, "upper.tmpl", `[{{upper "abc"}}]`)
	htmlRender := HTMLDebug{
		Files:   []string{file},
		FuncMap: template.FuncMap{},
	}
	w := httptest.NewRecorder()
	instance := htmlRender.InstanceWithFuncs("upper.tmpl", nil, template.FuncMap{
		"upper": strings.ToUpper,
	})
	require.NoError(t, instance.Render(w))
	assert.Equal(t, "[ABC]", w.Body.String())
}

func TestHTMLWithFuncsBoundIsStable(t *testing.T) {
	file := writeTemplateFile(t, "upper.tmpl", `[{{upper "abc"}}]`)
	htmlRender := HTMLDebug{Files: []string{file}}
	instance := htmlRender.InstanceWithFuncs("upper.tmpl", nil, template.FuncMap{
		"upper": strings.ToUpper,
	})

	// Re-applying funcs to an already bound instance returns it unchanged.
	same := instance.(HTML).WithFuncs(template.FuncMap{"upper": strings.ToLower})
	assert.Equal(t, instance, same)

	w := httptest.NewRecorder()
	require.NoError(t, same.Render(w))
	assert.Equal(t, "[ABC]", w.Body.String())
}

func TestParseSourcesSyntaxError(t *testing.T) {
	file := writeTemplateFile(t, "broken.tmpl", `{{`)
	production := HTMLProduction{Glob: filepath.Join(filepath.Dir(file), "*")}
	_, err := production.ParseSources()
	require.Error(t, err)
}

func TestParseSourcesNoSources(t *testing.T) {
	_, err := HTMLProduction{Template: template.Must(template.New("x").Parse("ok"))}.ParseSources()
	require.ErrorIs(t, err, errHTMLNoSources)
}
