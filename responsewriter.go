package updateprompt

import (
	"bufio"
	"io"
	"net"
	"net/http"
)

// statusRecorder notes the status a handler answers with
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *statusRecorder) WriteHeader(status int) {
	// 1xx are informational; the final status comes later
	if recorder.status == 0 && status >= 200 {
		recorder.status = status
	}
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *statusRecorder) Write(data []byte) (int, error) {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	return recorder.ResponseWriter.Write(data)
}

// Unwrap lets http.ResponseController reach the writer below
func (recorder *statusRecorder) Unwrap() http.ResponseWriter {
	return recorder.ResponseWriter
}

// ReadFrom keeps the fast path for files, such as sendfile, when the writer
// below has one
func (recorder *statusRecorder) ReadFrom(src io.Reader) (int64, error) {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	if readerFrom, ok := recorder.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(src)
	}
	return io.Copy(writerOnly{recorder.ResponseWriter}, src)
}

// writerOnly hides a writer's ReadFrom, so io.Copy doesn't call it again
type writerOnly struct{ io.Writer }

// wrap returns the recorder as a writer that is an http.Flusher and an
// http.Hijacker only when the writer below is, so handlers that check for
// them still choose correctly
func (recorder *statusRecorder) wrap() http.ResponseWriter {
	_, flusher := recorder.ResponseWriter.(http.Flusher)
	_, hijacker := recorder.ResponseWriter.(http.Hijacker)
	switch {
	case flusher && hijacker:
		return &flushHijackRecorder{recorder}
	case flusher:
		return &flushRecorder{recorder}
	case hijacker:
		return &hijackRecorder{recorder}
	default:
		return recorder
	}
}

type flushRecorder struct{ *statusRecorder }

func (recorder *flushRecorder) Flush() { recorder.flush() }

type hijackRecorder struct{ *statusRecorder }

func (recorder *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return recorder.hijack()
}

type flushHijackRecorder struct{ *statusRecorder }

func (recorder *flushHijackRecorder) Flush() { recorder.flush() }

func (recorder *flushHijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return recorder.hijack()
}

func (recorder *statusRecorder) flush() {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	recorder.ResponseWriter.(http.Flusher).Flush()
}

func (recorder *statusRecorder) hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := recorder.ResponseWriter.(http.Hijacker).Hijack()
	if err == nil && recorder.status == 0 {
		recorder.status = http.StatusSwitchingProtocols
	}
	return conn, rw, err
}
