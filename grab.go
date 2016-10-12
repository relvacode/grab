// Package grab is a utility for downloading large files from remote HTTP servers that support byte range headers.
package grab

import (
	"fmt"
	"github.com/pkg/errors"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"syscall"
	"time"
)

// ReadCloseSeeker is an interface that wraps io.ReadCloser and io.Seeker.
type ReadCloseSeeker interface {
	io.ReadCloser
	io.Seeker
}

var (
	// Log is the logger used to print errors in retry attempts
	Log = log.New(ioutil.Discard, "s3  ", 3)
)

var (
	// DefaultClient is the HTTP client used for downloading.
	DefaultClient = ClientWithTimeout(time.Second * 10)

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

// Grab begins downloading the given URL.
func Grab(u string) (ReadCloseSeeker, error) {
	return GrabOptions(u, DefaultAttempts, DefaultClient, nil)
}

// GrabOptions begins downloading the given URL with custom options.
func GrabOptions(u string, n int, c *http.Client, h http.Header) (ReadCloseSeeker, error) {
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
	return &body{c: c, body: resp.Body, tPos: resp.ContentLength, req: req, n: n}, nil
}

// Body is a wrapper http response body
type body struct {
	c *http.Client
	n int

	req  *http.Request
	body io.ReadCloser

	cPos int64
	tPos int64

	closed bool
	err    error
}

func (b *body) Close() error {
	if b.closed {
		return syscall.EINVAL
	}
	b.closed = true
	if b.body != nil {
		return b.body.Close()
	}
	return nil
}

func (b *body) nextReader() error {
	n := b.cPos + 1
	resp, err := retry(b.req, b.c, b.n, &n)
	if err != nil {
		return err
	}
	// Check response headers are valid
	if _, ok := resp.Header["Content-Range"]; !ok {
		resp.Body.Close()
		return errors.New("missing Content-Range header in response")
	}
	b.body = resp.Body
	return nil
}

func (b *body) read(p []byte) (n int, err error) {
	for i := 0; i < b.n; i++ {
		n, err = b.body.Read(p)
		b.cPos += int64(n)
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

func (b *body) Read(p []byte) (int, error) {
	if b.closed {
		return 0, syscall.EINVAL
	}
	if b.err != nil {
		return 0, b.err
	}

	if b.cPos == b.tPos {
		return 0, io.EOF
	}

	if b.cPos > b.tPos {
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

func (b *body) Seek(offset int64, whence int) (int64, error) {
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

	b.cPos = pos
	b.body.Close()
	b.body = nil
	return b.cPos, nil
}
