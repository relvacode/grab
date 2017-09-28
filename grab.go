// Package grab is a utility for downloading large files from remote HTTP servers that support byte range headers.
package grab

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"syscall"
	"time"

	"github.com/pkg/errors"
)

// ReadCloseSeeker is an interface that wraps io.ReadCloser and io.Seeker.
type ReadCloseSeeker interface {
	io.ReadCloser
	io.Seeker
}

var (
	// Log is the logger used to print errors in retry attempts
	Log = log.New(ioutil.Discard, "", 3)
)

var (
	// DefaultClient is the HTTP client used for downloading.
	DefaultClient = ClientWithTimeout(time.Second*10, true)

	// DefaultAttempts is the number of default retries before giving up.
	DefaultAttempts = 5
)

func retry(req *http.Request, c *http.Client, n int, rng *int64) (resp *http.Response, err error) {
	if rng != nil {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", *rng))
	}
	for i := 0; i < n; i++ {
		resp, err = c.Do(req)
		if err == nil {
			err = checkResponse(resp)
		}
		if err == nil {
			return
		}
		Log.Printf("http attempt %d: %s\n", i, err)
		sleep(i + 1)
	}
	err = errors.Wrapf(err, "request failed after %d attempts", n)
	return
}

// Open begins downloading the given URL.
func Open(u string) (*Body, error) {
	return OpenWith(u, DefaultAttempts, DefaultClient, nil)
}

// OpenWith begins downloading the given URL with custom options.
func OpenWith(u string, n int, c *http.Client, h http.Header) (*Body, error) {
	if c == nil {
		c = DefaultClient
	}
	if n == 0 {
		n = DefaultAttempts
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	// Copy global headers from client
	if h != nil {
		for k, v := range h {
			req.Header[k] = v
		}
	}
	resp, err := retry(req, c, n, nil)
	if err != nil {
		return nil, err
	}
	// If new URL is different then cache the new URL for further requests.
	// Prevents having to follow a redirect for every retry.
	if resp.Request.URL.String() != req.URL.String() {
		req.URL = resp.Request.URL
	}
	return &Body{c: c, body: resp.Body, tPos: resp.ContentLength, req: req, n: n}, nil
}

// Ensure Body implements ReadCloseSeeker
var _ ReadCloseSeeker = &Body{}

// Body is a wrapper http response body
type Body struct {
	c *http.Client
	n int

	req  *http.Request
	body io.ReadCloser

	cPos int64
	tPos int64

	closed bool
	err    error
}

// Len returns the total length in bytes of the content.
func (b *Body) Len() int64 {
	return b.tPos
}

// Close closes the currently opened body.
func (b *Body) Close() error {
	if b.closed {
		return syscall.EINVAL
	}
	b.closed = true
	if b.body != nil {
		return b.body.Close()
	}
	return nil
}

func (b *Body) nextReader() error {
	// If not reading for the first time
	// set the start range to the current position
	var n *int64
	if b.cPos > 0 {
		n = &b.cPos
	}
	resp, err := retry(b.req, b.c, b.n, n)
	if err != nil {
		return err
	}
	// Check response headers are valid
	r := resp.Header.Get("Content-Range")
	if r == "" {
		resp.Body.Close()
		return errors.New("missing Content-Range header in response")
	}

	b.body = resp.Body
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (b *Body) read(p []byte) (n int, err error) {
	for i := 0; i < b.n; i++ {
		var rn int
		rn, err = b.body.Read(p[n:])
		n += rn
		b.cPos += int64(rn)

		// A non EOF error occurred but we have enough data anyway
		if err != io.EOF && b.cPos == b.tPos {
			return n, err
		}
		if err == nil || err == io.EOF {
			return
		}
		Log.Printf("read attempt %d: %s", i, err)
		sleep(i + 1)
		b.body.Close()
		b.body = nil

		if err = b.nextReader(); err != nil {
			return
		}
	}
	err = errors.Errorf("unable to read from request body after %d attempts", b.n)
	return
}

func (b *Body) Read(p []byte) (int, error) {
	if b.closed {
		return 0, syscall.EINVAL
	}
	if b.err != nil {
		return 0, b.err
	}

	if b.cPos == b.tPos+1 {
		return 0, io.EOF
	}

	if b.cPos > b.tPos+1 {
		return 0, errors.Errorf("read at position %d past %d boundary", b.cPos, b.tPos)
	}

	if b.body == nil {
		if err := b.nextReader(); err != nil {
			return 0, err
		}
	}

	nw := 0
	for nw < len(p) {
		n, err := b.read(p[nw:])
		nw += n
		if err == io.EOF {
			b.body = nil
			if b.cPos == b.tPos {
				return nw, io.EOF
			}
			if err := b.nextReader(); err != nil {
				return nw, err
			}
			continue
		}
		if err != nil {
			b.err = err
			return nw, err
		}
	}
	return nw, nil
}

// Seek seeks to the requested position.
// If the new position is different then the current body is closed and discarded,
// A new request is made for the new position on the next read call.
func (b *Body) Seek(offset int64, whence int) (int64, error) {
	pos := b.cPos
	switch whence {
	case io.SeekCurrent:
		pos = b.cPos + offset
	case io.SeekStart:
		pos = 0 + offset
	case io.SeekEnd:
		pos = b.tPos + offset
	}

	if pos < 0 {
		return b.cPos, errors.New("cannot seek before beginning")
	}

	// Do not seek if new position is the same
	if b.cPos == pos {
		return b.cPos, nil
	}

	b.cPos = pos
	if b.body != nil {
		b.body.Close()
		b.body = nil
	}
	return b.cPos, nil
}
