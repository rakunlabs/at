package server

import (
	"bufio"
	"io"
	"net/http"
	"strings"
)

// pipeResponseWriter streams a handler's output to a reader, like a live
// HTTP response, so a test can read SSE events while the handler runs.
type pipeResponseWriter struct {
	header http.Header
	w      *io.PipeWriter
}

func ioPipeWriter() (*io.PipeReader, *pipeResponseWriter) {
	r, w := io.Pipe()
	return r, &pipeResponseWriter{header: http.Header{}, w: w}
}

func (p *pipeResponseWriter) Header() http.Header         { return p.header }
func (p *pipeResponseWriter) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeResponseWriter) WriteHeader(int)             {}
func (p *pipeResponseWriter) Flush()                      {}
func (p *pipeResponseWriter) Close()                      { _ = p.w.Close() }

func readAll(r *bufio.Reader) (string, error) {
	var b strings.Builder
	_, err := io.Copy(&b, r)
	return b.String(), err
}
