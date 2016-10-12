package grab

import (
	"fmt"
	"github.com/pkg/errors"
	"io"
	"log"
	"net/http"
	"os"
	"syscall"
	"time"
)

type ReadCloseSeeker interface {
	io.Reader
	io.Closer
	io.Seeker
}

var (
	Logger = log.New(os.Stderr, "s3  ", 3)
)

var (
	// DefaultHTTPClient is the HTTP client used by the range client.
	DefaultHTTPClient = HTTPClientWithTimeout(time.Second * 10)

	// DefaultAttempts is the number of default retries before giving up.
	DefaultAttempts = 5
)

// NewClient creates a new range client.
func NewClient() *Client {
	return &Client{
		HTTP:     DefaultHTTPClient,
		Attempts: DefaultAttempts,
	}
}

// Client
type Client struct {
	HTTP *http.Client

	// Optional global headers
	Headers http.Header

	// Number of attempts before giving up
	Attempts int
}

func (c *Client) retry(req *http.Request, rng *int64) (resp *http.Response, err error) {
	if rng != nil {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", *rng))
	}
	for i := 0; i < c.Attempts; i++ {
		resp, err = c.HTTP.Do(req)
		if err == nil {
			err = checkResponse(resp)
		}
		if err == nil {
			return
		}
		Logger.Printf("http attempt %d: %s\n", i, err)
		sleep(i + 1)
	}
	err = errors.Wrapf(err, "request failed after %d attempts", c.Attempts)
	return
}

// Being downloading the given url
func (c *Client) Download(u string) (ReadCloseSeeker, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	// Copy global headers from client
	if c.Headers != nil {
		for k, v := range c.Headers {
			req.Header[k] = v
		}
	}
	resp, err := c.retry(req, nil)
	if err != nil {
		return nil, err
	}
	return &Body{Client: c, body: resp.Body, tPos: resp.ContentLength, req: req}, nil
}

type Body struct {
	Client *Client

	req  *http.Request
	body io.ReadCloser

	cPos int64
	tPos int64

	closed bool
	err    error
}

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
	n := b.cPos + 1
	resp, err := b.Client.retry(b.req, &n)
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

func (b *Body) read(p []byte) (n int, err error) {
	for i := 0; i < b.Client.Attempts; i++ {
		n, err = b.body.Read(p)
		b.cPos += int64(n)
		if err == nil || err == io.EOF {
			return
		}
		Logger.Printf("read attempt %d: %s", i, err)
		sleep(i + 1)
		b.body.Close()
		b.body = nil
		if err = b.nextReader(); err != nil {
			return
		}
	}
	err = errors.Errorf("unable to read from request body after %d attempts", b.Client.Attempts)
	return
}

func (b *Body) Read(p []byte) (int, error) {
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

	b.cPos = pos
	b.body.Close()
	b.body = nil
	return b.cPos, nil
}
