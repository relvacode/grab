package grab

import (
	"bytes"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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

	rd, err := io.Copy(ioutil.Discard, g)
	if err != nil {
		t.Fatal(err)
	}
	if int64(rd) != testContentLength {
		t.Fatalf("expected %d bytes read, got %d", testContentLength, rd)
	}
}
