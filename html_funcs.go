// Copyright 2024 Gin Core Team. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

package gin

import "html/template"

// HTMLFuncFrame is one frame of the request-scoped HTML render function
// stack. Frames are independent of the engine-wide FuncMap and of every other
// request: they live on the Context and are gone once the request ends.
type HTMLFuncFrame struct {
	// FuncMap holds the template function names contributed or overridden by
	// this frame. A nil or empty FuncMap attaches nothing.
	FuncMap template.FuncMap
}

// PushHTMLFuncs pushes one frame onto the request-scoped HTML function stack.
// The frame is copied, so later mutation of the passed map does not affect the
// frame. The returned function pops exactly this frame; it is safe to defer
// and safe to call more than once.
//
// The typical usage is:
//
//	defer c.PushHTMLFuncs(gin.HTMLFuncFrame{FuncMap: template.FuncMap{
//		"money": money,
//	}})()
//
// Frames are merged with the engine baseline FuncMap before every HTML render,
// inner frames overriding outer ones and the engine baseline.
func (c *Context) PushHTMLFuncs(frame HTMLFuncFrame) func() {
	funcs := make(template.FuncMap, len(frame.FuncMap))
	for name, fn := range frame.FuncMap {
		funcs[name] = fn
	}
	c.htmlFuncStack = append(c.htmlFuncStack, HTMLFuncFrame{FuncMap: funcs})
	return c.popHTMLFuncs
}

// PopHTMLFuncs removes the topmost frame from the request-scoped HTML function
// stack. It is a no-op when the stack is empty.
func (c *Context) PopHTMLFuncs() {
	c.popHTMLFuncs()
}

func (c *Context) popHTMLFuncs() {
	if n := len(c.htmlFuncStack); n > 0 {
		c.htmlFuncStack[n-1] = HTMLFuncFrame{}
		c.htmlFuncStack = c.htmlFuncStack[:n-1]
	}
}

// WithHTMLFuncs pushes frame, runs fn and pops the frame again even if fn
// panics. The panic, if any, is re-panicked after the frame has been removed,
// so error paths never leak a frame into subsequent renders.
func (c *Context) WithHTMLFuncs(frame HTMLFuncFrame, fn func()) {
	pop := c.PushHTMLFuncs(frame)
	defer pop()
	fn()
}

// CurrentHTMLFuncs returns the template FuncMap currently visible to HTML
// rendering: a fresh map built from the engine baseline FuncMap with all
// request frames merged on top, the innermost frame winning on duplicate
// names. With no frame pushed it returns the engine baseline FuncMap, which
// is exactly what rendering uses without the stack.
func (c *Context) CurrentHTMLFuncs() template.FuncMap {
	if len(c.htmlFuncStack) == 0 {
		return c.engine.FuncMap
	}
	return c.mergedHTMLFuncs()
}

// mergedHTMLFuncs builds the effective FuncMap: engine baseline first, then
// every frame from the bottom to the top of the stack.
func (c *Context) mergedHTMLFuncs() template.FuncMap {
	merged := make(template.FuncMap, len(c.engine.FuncMap))
	for name, fn := range c.engine.FuncMap {
		merged[name] = fn
	}
	for _, frame := range c.htmlFuncStack {
		for name, fn := range frame.FuncMap {
			merged[name] = fn
		}
	}
	return merged
}

// resetHTMLFuncs drops all request frames. It is called when the request ends
// (and when a pooled Context is reused), so no frame can leak into renders
// performed after the request, including renders from background goroutines.
func (c *Context) resetHTMLFuncs() {
	for i := range c.htmlFuncStack {
		c.htmlFuncStack[i] = HTMLFuncFrame{}
	}
	c.htmlFuncStack = nil
}
