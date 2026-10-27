// Copyright 2024 Gin Core Team. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

package gin

import (
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupFuncsEngine builds an Engine whose templates are loaded from a
// temporary directory with LoadHTMLGlob.
func setupFuncsEngine(t *testing.T, mode string, files map[string]string, baseline template.FuncMap) (*Engine, string) {
	t.Helper()
	prevMode := Mode()
	SetMode(mode)
	t.Cleanup(func() { SetMode(prevMode) })

	dir := t.TempDir()
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}

	r := New()
	if baseline != nil {
		r.SetFuncMap(baseline)
	}
	r.LoadHTMLGlob(filepath.Join(dir, "*"))
	return r, dir
}

func serveRequest(r *Engine, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, target, nil)
	r.ServeHTTP(w, req)
	return w
}

func baselineWhoFuncMap() template.FuncMap {
	return template.FuncMap{"who": func() string { return "base" }}
}

// TestHTMLFuncsNewFuncAndOverride exercises adding a brand-new template func
// and overriding an existing one for one request, in every gin mode.
func TestHTMLFuncsNewFuncAndOverride(t *testing.T) {
	templates := map[string]string{
		// "who" exists on the engine baseline; "upper" is brand new.
		"who.tmpl":   `{{who}}-{{upper "abc"}}`,
		"upper.tmpl": `[{{upper "abc"}}]`,
	}
	for _, mode := range []string{DebugMode, TestMode, ReleaseMode} {
		t.Run(mode, func(t *testing.T) {
			r, _ := setupFuncsEngine(t, mode, templates, baselineWhoFuncMap())

			r.GET("/with", func(c *Context) {
				defer c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{
					"who":   func() string { return "request" },
					"upper": strings.ToUpper,
				}})()
				c.HTML(http.StatusOK, "who.tmpl", nil)
			})
			r.GET("/with-blob", func(c *Context) {
				c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{
					"who":   func() string { return "request-blob" },
					"upper": strings.ToUpper,
				}})
				defer c.PopHTMLFuncs()
				c.HTMLBlob(http.StatusOK, "who.tmpl", nil)
			})
			r.GET("/new-only", func(c *Context) {
				defer c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{"upper": strings.ToUpper}})()
				c.HTML(http.StatusOK, "upper.tmpl", nil)
			})
			var withoutCtx *Context
			r.GET("/without", func(c *Context) {
				withoutCtx = c
				c.HTML(http.StatusOK, "who.tmpl", nil)
			})

			assert.Equal(t, "request-ABC", serveRequest(r, "/with").Body.String())
			assert.Equal(t, "request-blob-ABC", serveRequest(r, "/with-blob").Body.String())
			assert.Equal(t, "[ABC]", serveRequest(r, "/new-only").Body.String())

			if mode != ReleaseMode {
				// In debug/test mode the strict per-request parse of a template
				// that names a func nobody supplied keeps panicking, as before.
				return
			}
			// No frame: baseline "who" renders, the missing "upper" func errors
			// at execution, so the request func never leaks into the output.
			w := serveRequest(r, "/without")
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "base-", w.Body.String())
			require.NotNil(t, withoutCtx)
			require.Len(t, withoutCtx.Errors, 1)
			assert.Contains(t, withoutCtx.Errors[0].Error(), "upper")
		})
	}
}

// TestHTMLFuncsNestedStack verifies inner frames override outer frames and
// popping restores the outer view.
func TestHTMLFuncsNestedStack(t *testing.T) {
	files := map[string]string{"nested.tmpl": `{{who}}-{{upper "x"}}`}
	baseline := template.FuncMap{
		"who":   func() string { return "base" },
		"upper": strings.ToUpper,
	}
	r, _ := setupFuncsEngine(t, ReleaseMode, files, baseline)

	r.GET("/nested", func(c *Context) {
		popOuter := c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{
			"who": func() string { return "outer" },
		}})
		c.HTML(http.StatusOK, "nested.tmpl", nil)

		popInner := c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{
			"who": func() string { return "inner" },
		}})
		c.HTML(http.StatusOK, "nested.tmpl", nil)

		popInner()
		c.HTML(http.StatusOK, "nested.tmpl", nil)

		popOuter()
		c.HTML(http.StatusOK, "nested.tmpl", nil)
	})

	// Four renders in one response: outer, inner, outer restored, baseline.
	assert.Equal(t, "outer-Xinner-Xouter-Xbase-X", serveRequest(r, "/nested").Body.String())
}

// TestHTMLFuncsWithScopePanic ensures WithHTMLFuncs pops even on panic.
func TestHTMLFuncsWithScopePanic(t *testing.T) {
	r := New()
	w := httptest.NewRecorder()
	c := CreateTestContextOnly(w, r)

	frame := HTMLFuncFrame{FuncMap: template.FuncMap{"who": func() string { return "scoped" }}}
	require.NotPanics(t, func() {
		defer func() { _ = recover() }()
		c.WithHTMLFuncs(frame, func() { panic("boom") })
	})
	assert.Empty(t, c.htmlFuncStack)
	assert.Equal(t, r.FuncMap, c.CurrentHTMLFuncs())

	// The pop closure is idempotent.
	pop := c.PushHTMLFuncs(frame)
	pop()
	pop()
	assert.Empty(t, c.htmlFuncStack)
}

// TestHTMLFuncsMergeQuery verifies the merge rules: baseline plus frames,
// nearest wins, nil/empty frames attach nothing, engine map is never mutated.
func TestHTMLFuncsMergeQuery(t *testing.T) {
	r := New()
	r.SetFuncMap(template.FuncMap{"a": 1, "b": 1})
	w := httptest.NewRecorder()
	c := CreateTestContextOnly(w, r)

	// No frame: the engine baseline itself is visible.
	assert.Equal(t, r.FuncMap, c.CurrentHTMLFuncs())

	c.PushHTMLFuncs(HTMLFuncFrame{}) // nil FuncMap
	c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{"b": 2, "c": 3}})
	assert.Equal(t, template.FuncMap{"a": 1, "b": 2, "c": 3}, c.CurrentHTMLFuncs())

	c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{"b": 4}})
	assert.Equal(t, template.FuncMap{"a": 1, "b": 4, "c": 3}, c.CurrentHTMLFuncs())

	c.PopHTMLFuncs()
	assert.Equal(t, template.FuncMap{"a": 1, "b": 2, "c": 3}, c.CurrentHTMLFuncs())

	// The engine baseline map itself must stay untouched.
	assert.Equal(t, template.FuncMap{"a": 1, "b": 1}, r.FuncMap)
}

// TestHTMLFuncsFrameIsCopied ensures later mutation of a pushed map cannot
// change the frame.
func TestHTMLFuncsFrameIsCopied(t *testing.T) {
	r := New()
	c := CreateTestContextOnly(httptest.NewRecorder(), r)

	frameMap := template.FuncMap{"x": 1}
	c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: frameMap})
	frameMap["x"] = 2
	frameMap["y"] = 3

	assert.Equal(t, template.FuncMap{"x": 1}, c.CurrentHTMLFuncs())
}

// TestHTMLFuncsDirectRenderPath ensures funcs are merged even when rendering
// goes through the generic Render interface with a manually created instance.
func TestHTMLFuncsDirectRenderPath(t *testing.T) {
	files := map[string]string{"upper.tmpl": `[{{upper "abc"}}]`}
	r, _ := setupFuncsEngine(t, ReleaseMode, files, nil)

	r.GET("/direct", func(c *Context) {
		defer c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{"upper": strings.ToUpper}})()
		instance := c.engine.HTMLRender.Instance("upper.tmpl", nil)
		c.Render(http.StatusOK, instance)
	})

	assert.Equal(t, "[ABC]", serveRequest(r, "/direct").Body.String())
}

// TestHTMLFuncsSetHTMLTemplateOverride covers the SetHTMLTemplate source-less
// path: per-request funcs can override names known at parse time, and the
// engine-wide template is never mutated.
func TestHTMLFuncsSetHTMLTemplateOverride(t *testing.T) {
	SetMode(ReleaseMode)
	defer SetMode(TestMode)

	r := New()
	r.SetFuncMap(template.FuncMap{"who": func() string { return "base" }})
	templ := template.Must(template.New("who.tmpl").Funcs(r.FuncMap).Parse(`{{who}}`))
	r.SetHTMLTemplate(templ)

	r.GET("/override", func(c *Context) {
		defer c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{"who": func() string { return "request" }}})()
		c.HTML(http.StatusOK, "who.tmpl", nil)
	})
	r.GET("/plain", func(c *Context) {
		c.HTML(http.StatusOK, "who.tmpl", nil)
	})

	assert.Equal(t, "request", serveRequest(r, "/override").Body.String())
	assert.Equal(t, "base", serveRequest(r, "/plain").Body.String())

	// The template stored on the engine is unchanged.
	var buf strings.Builder
	require.NoError(t, templ.ExecuteTemplate(&buf, "who.tmpl", nil))
	assert.Equal(t, "base", buf.String())
}

// TestHTMLFuncsLoadHTMLFS covers the FS loader with request-scoped funcs.
func TestHTMLFuncsLoadHTMLFS(t *testing.T) {
	SetMode(ReleaseMode)
	defer SetMode(TestMode)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "upper.tmpl"), []byte(`[{{upper "abc"}}]`), 0o644))

	r := New()
	r.LoadHTMLFS(http.Dir(dir), "upper.tmpl")
	r.GET("/fs", func(c *Context) {
		defer c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{"upper": strings.ToUpper}})()
		c.HTML(http.StatusOK, "upper.tmpl", nil)
	})

	assert.Equal(t, "[ABC]", serveRequest(r, "/fs").Body.String())
}

// TestHTMLFuncsLoadFiles covers LoadHTMLFiles in release mode.
func TestHTMLFuncsLoadFiles(t *testing.T) {
	SetMode(ReleaseMode)
	defer SetMode(TestMode)

	dir := t.TempDir()
	file := filepath.Join(dir, "upper.tmpl")
	require.NoError(t, os.WriteFile(file, []byte(`[{{upper "abc"}}]`), 0o644))

	r := New()
	r.LoadHTMLFiles(file)
	r.GET("/f", func(c *Context) {
		defer c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{"upper": strings.ToUpper}})()
		c.HTML(http.StatusOK, "upper.tmpl", nil)
	})

	assert.Equal(t, "[ABC]", serveRequest(r, "/f").Body.String())
}

// TestHTMLFuncsSyntaxErrorStillPanics ensures hard parse errors still surface
// eagerly at Load time.
func TestHTMLFuncsSyntaxErrorStillPanics(t *testing.T) {
	SetMode(ReleaseMode)
	defer SetMode(TestMode)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.tmpl"), []byte(`{{`), 0o644))

	r := New()
	assert.Panics(t, func() { r.LoadHTMLGlob(filepath.Join(dir, "*")) })
}

// TestHTMLFuncsEscaping verifies contextual escaping stays correct for funcs
// that were unknown at load time.
func TestHTMLFuncsEscaping(t *testing.T) {
	files := map[string]string{"danger.tmpl": `{{danger}}`}
	r, _ := setupFuncsEngine(t, ReleaseMode, files, nil)

	r.GET("/escaped", func(c *Context) {
		defer c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{"danger": func() string { return "<i>" }}})()
		c.HTML(http.StatusOK, "danger.tmpl", nil)
	})
	r.GET("/raw", func(c *Context) {
		defer c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{"danger": func() template.HTML { return template.HTML("<i>") }}})()
		c.HTML(http.StatusOK, "danger.tmpl", nil)
	})

	assert.Equal(t, "&lt;i&gt;", serveRequest(r, "/escaped").Body.String())
	assert.Equal(t, "<i>", serveRequest(r, "/raw").Body.String())
}

// TestHTMLFuncsNonHTMLExitsUnchanged verifies JSON/XML/YAML and static files do
// not subscribe to the stack.
func TestHTMLFuncsNonHTMLExitsUnchanged(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "static.txt"), []byte("static-bytes"), 0o644))

	r := New()
	r.StaticFile("/static", filepath.Join(dir, "static.txt"))
	r.GET("/json", func(c *Context) {
		defer c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{"who": func() string { return "SHOULD-NOT-MATTER" }}})()
		c.JSON(http.StatusOK, H{"x": 1})
	})
	r.GET("/yaml", func(c *Context) {
		defer c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{"who": func() string { return "SHOULD-NOT-MATTER" }}})()
		c.YAML(http.StatusOK, H{"x": 1})
	})
	r.GET("/xml", func(c *Context) {
		defer c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{"who": func() string { return "SHOULD-NOT-MATTER" }}})()
		c.XML(http.StatusOK, H{"x": "1"})
	})
	r.GET("/data", func(c *Context) {
		defer c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{"who": func() string { return "SHOULD-NOT-MATTER" }}})()
		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte("raw"))
	})

	w := serveRequest(r, "/json")
	assert.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Equal(t, `{"x":1}`, w.Body.String())

	w = serveRequest(r, "/yaml")
	assert.Equal(t, "x: 1\n", w.Body.String())

	w = serveRequest(r, "/xml")
	assert.Equal(t, "application/xml; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), "<x>1</x>")

	w = serveRequest(r, "/data")
	assert.Equal(t, "raw", w.Body.String())

	w = serveRequest(r, "/static")
	assert.Equal(t, "static-bytes", w.Body.String())
}

// TestHTMLFuncsConcurrentRequests verifies two concurrent requests pushing
// different funcs only ever see their own stack top, including side effects.
func TestHTMLFuncsConcurrentRequests(t *testing.T) {
	files := map[string]string{"who.tmpl": `{{who}}`}
	r, _ := setupFuncsEngine(t, ReleaseMode, files, nil)

	var aHits, bHits atomic.Int64
	r.GET("/a", func(c *Context) {
		defer c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{"who": func() string {
			aHits.Add(1)
			return "A"
		}}})()
		c.HTML(http.StatusOK, "who.tmpl", nil)
	})
	r.GET("/b", func(c *Context) {
		defer c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{"who": func() string {
			bHits.Add(1)
			return "B"
		}}})()
		c.HTML(http.StatusOK, "who.tmpl", nil)
	})

	ts := httptest.NewServer(r)
	defer ts.Close()

	const n = 60
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for range n {
		wg.Add(2)
		go func() {
			defer wg.Done()
			res, err := http.Get(ts.URL + "/a")
			if err != nil {
				errs <- err
				return
			}
			defer res.Body.Close()
			buf := make([]byte, 16)
			read, _ := res.Body.Read(buf)
			if string(buf[:read]) != "A" {
				errs <- errors.New("got non-A body on /a: " + string(buf[:read]))
			}
		}()
		go func() {
			defer wg.Done()
			res, err := http.Get(ts.URL + "/b")
			if err != nil {
				errs <- err
				return
			}
			defer res.Body.Close()
			buf := make([]byte, 16)
			read, _ := res.Body.Read(buf)
			if string(buf[:read]) != "B" {
				errs <- errors.New("got non-B body on /b: " + string(buf[:read]))
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	assert.EqualValues(t, n, aHits.Load())
	assert.EqualValues(t, n, bHits.Load())
}

// TestHTMLFuncsBackgroundGoroutineSeesEmptyStack verifies that after a request
// ends, even an un-popped frame cannot influence a later render performed by a
// background goroutine still holding the Context.
func TestHTMLFuncsBackgroundGoroutineSeesEmptyStack(t *testing.T) {
	files := map[string]string{"who.tmpl": `{{who}}`}
	r, _ := setupFuncsEngine(t, ReleaseMode, files, baselineWhoFuncMap())

	w := httptest.NewRecorder()
	started := make(chan struct{})
	release := make(chan struct{})
	var done sync.WaitGroup
	done.Add(1)

	req, _ := http.NewRequest(http.MethodGet, "/leak", nil)
	r.GET("/leak", func(c *Context) {
		// Deliberately pushed but never popped.
		c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{"who": func() string { return "LEAK" }}})
		go func() {
			started <- struct{}{}
			<-release
			defer done.Done()
			c.HTML(http.StatusOK, "who.tmpl", nil)
		}()
		<-started
		c.HTML(http.StatusOK, "who.tmpl", nil)
	})

	r.ServeHTTP(w, req) // returning here means the request and its cleanup are done

	close(release)
	done.Wait()

	// First render saw the frame; the post-request render saw the baseline.
	assert.Equal(t, "LEAKbase", w.Body.String())
}

// TestHTMLFuncsCopyHasEmptyStack verifies Context.Copy, intended for goroutine
// use, never inherits request frames.
func TestHTMLFuncsCopyHasEmptyStack(t *testing.T) {
	r := New()
	r.SetFuncMap(template.FuncMap{"who": func() string { return "base" }})
	c := CreateTestContextOnly(httptest.NewRecorder(), r)
	c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{"who": func() string { return "copy?" }}})

	cp := c.Copy()
	assert.Equal(t, r.FuncMap, cp.CurrentHTMLFuncs())
	assert.Empty(t, cp.htmlFuncStack)
}

// TestHTMLFuncsNoRendererConfigured ensures the funcs path keeps returning the
// "html renderer is not configured" error when no renderer is configured.
func TestHTMLFuncsNoRendererConfigured(t *testing.T) {
	r := New()
	var ctx *Context
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/x", nil)
	r.GET("/x", func(c *Context) {
		ctx = c
		defer c.PushHTMLFuncs(HTMLFuncFrame{FuncMap: template.FuncMap{"who": func() string { return "x" }}})()
		c.HTML(http.StatusOK, "who.tmpl", nil)
	})
	r.ServeHTTP(w, req)

	require.NotNil(t, ctx)
	require.Len(t, ctx.Errors, 1)
	assert.Contains(t, ctx.Errors[0].Error(), "html renderer is not configured")
	assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Empty(t, w.Body.String())
}
