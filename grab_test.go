package grab

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"hash"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/pkg/errors"
)

func init() {
	Log.SetOutput(os.Stderr)
}

var testContent = []byte(`
Lorem ipsum dolor sit amet, consectetur adipiscing elit.
Morbi ut magna nec lectus faucibus malesuada ac eu erat.
Aliquam erat volutpat. Aliquam posuere, nunc vel volutpat posuere, nisi libero bibendum orci, non semper nibh augue quis orci.
Sed mattis pulvinar tortor, at tempus est volutpat in. Proin vel felis congue, porttitor nunc in, feugiat elit.
Sed aliquet convallis augue, vel eleifend augue interdum in.
Suspendisse dapibus, augue nec malesuada elementum, justo magna ullamcorper ligula, blandit efficitur urna enim sed ante.
Etiam tempor sodales odio, quis faucibus enim cursus quis.
Donec at nunc ac enim commodo rhoncus. Sed sagittis tellus eget ex pretium, eu commodo velit semper.
Vivamus pretium diam in eros euismod, nec ultricies leo tincidunt.
Pellentesque mauris libero, mattis vel leo et, viverra euismod magna.
`)

var testContentLength = int64(len(testContent))

var testContentHash hash.Hash

func init() {
	testContentHash = md5.New()
	testContentHash.Write(testContent)
}

type MockServer struct {
}

func (srv *MockServer) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	http.ServeContent(rw, r, "", time.Time{}, bytes.NewReader(testContent))
}

func TestGrab(t *testing.T) {
	srv := httptest.NewServer(&MockServer{})
	defer srv.Close()

	g, err := Open(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Check length of data
	if g.Len() != testContentLength {
		t.Fatalf("wanted response length %d, got %d", testContentLength, g.Len())
	}

	// Seek to last byte of data
	if pos, err := g.Seek(-1, io.SeekEnd); err != nil {
		t.Fatal(err)
	} else if pos != testContentLength-1 {
		t.Fatalf("invalid new seek position %d, want %d", pos, testContentLength-1)
	}

	// Read last byte of data
	b := make([]byte, 1)
	if rd, err := g.Read(b); err != nil && err != io.EOF {
		t.Fatal(err)
	} else if rd != 1 {
		t.Fatalf("expected 1 byte read, got %d", rd)
	}
	if lb := testContent[testContentLength-1]; b[0] != lb {
		t.Fatalf("wanted %d, got %d", lb, b[0])
	}

	if pos, err := g.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	} else if pos != 0 {
		t.Fatalf("invalid new seek position %d, wanted 0", pos)
	}

	compare := md5.New()

	rd, err := io.Copy(compare, g)
	if err != nil {
		t.Fatal(err)
	}
	if int64(rd) != testContentLength {
		t.Fatalf("expected %d bytes read, got %d", testContentLength, rd)
	}
	expect := fmt.Sprintf("%x", testContentHash.Sum(nil))
	got := fmt.Sprintf("%x", compare.Sum(nil))
	if expect != got {
		t.Fatalf("Expected MD5 hash of %q; got %q", expect, got)
	}
}

type BrokenReadSeeker struct {
	TotalLength int64

	r    *bytes.Reader
	read int
}

func (brs *BrokenReadSeeker) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekEnd:
		return brs.TotalLength, nil
	default:
		return brs.r.Seek(offset, whence)
	}
}

func (brs *BrokenReadSeeker) Read(b []byte) (int, error) {
	r, _ := brs.r.Read(b[:brs.TotalLength/4])
	return r, errors.New("ERROR")
}

func (brs *BrokenReadSeeker) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	http.ServeContent(rw, r, "", time.Time{}, brs)
}

func TestGrabDisconnect(t *testing.T) {
	expect := fmt.Sprintf("%x", testContentHash.Sum(nil))

	brs := &BrokenReadSeeker{
		TotalLength: testContentLength,
		r:           bytes.NewReader(testContent),
	}
	srv := httptest.NewServer(brs)
	defer srv.Close()

	g, err := Open(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	hash := md5.New()
	written, err := io.Copy(hash, g)
	if err != nil {
		t.Fatal(err)
	}
	if written != brs.TotalLength {
		t.Fatalf("Expected %d bytes written; got %d", brs.TotalLength, written)
	}

	got := fmt.Sprintf("%x", hash.Sum(nil))
	t.Log(got, expect)
	if got != expect {
		t.Fatalf("MD5 mismatch: Expected %q; got %q", expect, got)
	}
}

type AlwaysReadError struct {
	TotalLength int64
	r           *bytes.Reader
}

func (brs *AlwaysReadError) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekEnd:
		return brs.TotalLength, nil
	default:
		return brs.r.Seek(offset, whence)
	}
}

func (brs *AlwaysReadError) Read(b []byte) (int, error) {
	return 0, errors.New("ERROR")
}

func (brs *AlwaysReadError) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	http.ServeContent(rw, r, "", time.Time{}, brs)
}

func TestGrabDisconnectMustFatal(t *testing.T) {
	brs := &AlwaysReadError{
		TotalLength: testContentLength,
		r:           bytes.NewReader(testContent),
	}
	srv := httptest.NewServer(brs)
	defer srv.Close()

	g, err := Open(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	_, err = io.Copy(ioutil.Discard, g)
	if err == nil {
		t.Fatal("Expected an error but got nil")
	}
}
