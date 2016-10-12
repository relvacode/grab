package grab

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"github.com/pkg/errors"
	"io/ioutil"
	"math"
	"net/http"
	"time"
)

const backoff time.Duration = 600

func sleep(i int) {
	time.Sleep(time.Duration(math.Exp2(float64(i))) * backoff * time.Millisecond)
}

func checkResponse(r *http.Response) error {
	if r.StatusCode >= 300 {
		return newRespError(r)
	}
	if r.ContentLength == -1 {
		return errors.New("retrieving objects with undefined content-length responses (chunked transfer encoding / EOF close) is not supported")
	}
	return nil
}

func newRespError(r *http.Response) *RespError {
	defer r.Body.Close()
	e := new(RespError)
	e.StatusCode = r.StatusCode
	e.Code = r.Status
	if r.Header.Get("Content-Type") == "application/xml" {
		b, _ := ioutil.ReadAll(r.Body)
		xml.NewDecoder(bytes.NewReader(b)).Decode(e)
	}
	return e
}

// RespError represents an http error response from S3
// http://docs.aws.amazon.com/AmazonS3/latest/API/ErrorResponses.html
type RespError struct {
	Code       string
	Message    string
	Resource   string
	RequestID  string `xml:"RequestId"`
	StatusCode int
}

func (e *RespError) Error() string {
	return fmt.Sprintf(
		"[%s]: %s",
		e.Code,
		e.Message,
	)
}
