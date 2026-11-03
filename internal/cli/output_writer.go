package cli

import (
	"bytes"
	"fmt"
	"io"
	"sync"
)

// devOutput multiplexes the development session's log streams (A06 §9):
// meta's own messages, Air's process output, and Vite's process output all
// land on one terminal, so each line carries its source tag.
//
// Properties required by the blueprint:
//   - Complete messages pass through unmodified apart from the tag;
//     multiline compiler diagnostics keep one tag per line instead of being
//     collapsed or truncated.
//   - Nothing is buffered: each finished line is written immediately, so
//     there is no unbounded in-memory log and streaming stays live.
//   - Whole lines are written under a mutex, so two processes writing at
//     once cannot interleave inside a line.
//
// Partial-line chunks (a write not ending in \n) print the tag once when
// the line starts; subsequent chunks continue the same line until its
// newline arrives, so an Air "building..." progress rewrite or a split
// compiler line is neither duplicated nor lost.
type devOutput struct {
	w  io.Writer
	mu sync.Mutex
	// open tracks whether the last write left a line unfinished, so the
	// tag is printed once per line rather than once per chunk. Guarded by
	// mu; safe because one devOutput is shared by all writers.
	open bool
}

func newDevOutput(w io.Writer) *devOutput {
	return &devOutput{w: w}
}

// meta writes a meta-attributed line.
func (o *devOutput) meta(format string, a ...any) {
	o.line("[meta]", fmt.Sprintf(format, a...))
}

// airf returns the writer Air's process output streams to.
func (o *devOutput) air() io.Writer { return o.tagged("[air]") }

// vite returns the writer Vite's process output streams to.
func (o *devOutput) vite() io.Writer { return o.tagged("[vite]") }

func (o *devOutput) tagged(tag string) io.Writer {
	return &tagWriter{out: o, tag: tag}
}

// Write implements io.Writer for contexts that need a plain sink (the
// route reconciler's notices); lines written this way carry no tag.
func (o *devOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n, err := o.w.Write(p)
	return n, err
}

// line writes one attributed line under the output lock.
func (o *devOutput) line(tag, msg string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Fprintf(o.w, "%s %s\n", tag, msg)
}

func (o *devOutput) write(tag string, p []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for len(p) > 0 {
		nl := bytes.IndexByte(p, '\n')

		if !o.open {
			fmt.Fprintf(o.w, "%s ", tag)
			o.open = true
		}
		if nl < 0 {
			// Partial line: flush without newline so output stays live; the
			// next chunk continues the same tagged line.
			fmt.Fprintf(o.w, "%s", p)
			return
		}
		chunk := p[:nl]
		p = p[nl+1:]
		if len(chunk) > 0 && chunk[len(chunk)-1] == '\r' {
			chunk = chunk[:len(chunk)-1]
		}
		fmt.Fprintf(o.w, "%s\n", chunk)
		o.open = false
	}
}

// tagWriter turns arbitrary process output into attributed lines. It is the
// io.Writer handed to subprocess runners; concurrent Write calls are safe
// because devOutput.write serializes them.
type tagWriter struct {
	out *devOutput
	tag string
}

func (w *tagWriter) Write(p []byte) (int, error) {
	w.out.write(w.tag, p)
	return len(p), nil
}
