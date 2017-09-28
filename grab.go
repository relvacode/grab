// Package grab is a utility for downloading large files from remote HTTP servers that support byte range headers.
package grab

import (
	"crypto/md5"
	"fmt"
	"hash"
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

func tryParseETag(resp *http.Response) *string {
	header := resp.Header.Get("Etag")
	if header == "" {
		return nil
	}
	// Expect exactly 32 bytes for an MD5 digest
	// Only files uploaded as one block without multi-part upload are supported.
	if len(header) != 32 {
		return nil
	}

	return &header
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

	md5sum := md5.New()
	tee := io.TeeReader(resp.Body, md5sum)

	return &Body{
		ETag: tryParseETag(resp),
		md5:  md5sum,
		tee:  tee,
		c:    c,
		body: resp.Body,
		tPos: resp.ContentLength,
		req:  req,
		n:    n,
	}, nil
}

// Ensure Body implements ReadCloseSeeker
var _ ReadCloseSeeker = &Body{}

// Body is a wrapper http response body
type Body struct {
	ETag *string

	c *http.Client
	n int

	req  *http.Request
	body io.ReadCloser
	md5  hash.Hash
	tee  io.Reader

	cPos int64
	tPos int64

	closed bool
	seeked bool
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

// Sum returns the MD5 digest for the currently copied bytes from the server.
// NOTE: If body has been seeked then this value will not represent the contents of the entire file.
func (b *Body) Sum() []byte {
	return b.md5.Sum(nil)
}

// VerifyCopiedData checks the copied data and returns an error
// if the body ETag is set and the MD5 digest doesn't match the contents of the ETag header.
// Checking the ETag value is not supported for seeked reading (the file must be consumed only once in its entirety)
// Unless the body has been seeked to 0 and fully consumed, in which case the md5 hash is reset on call to Seek.
//
// Checking the value of the ETag is only supported if the file was not uploaded using a multi-part upload.
func (b *Body) VerifyCopiedData() error {
	if b.ETag == nil {
		return nil
	}
	if b.seeked {
		return errors.New("Cannot verify transfer for files that have been seeked")
	}
	digest := fmt.Sprintf("%x", b.Sum())
	if *b.ETag != digest {
		return errors.Errorf("ETag: Server reported ETag of %q but we calculated a digest of %q", *b.ETag, digest)
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

	// Setup the new TeeReader to read from this response body instead
	b.tee = io.TeeReader(resp.Body, b.md5)
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
		rn, err = b.tee.Read(p[n:])
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
// If the new seek position is 0 the md5 hash of the file is reset.
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

	if pos == 0 {
		b.seeked = false
		b.md5.Reset()
	} else {
		b.seeked = true
	}

	b.cPos = pos
	if b.body != nil {
		b.body.Close()
		b.body = nil
	}
	return b.cPos, nil
}
