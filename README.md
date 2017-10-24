# Grab

[![GoDoc](https://godoc.org/github.com/relvacode/grab?status.svg)](https://godoc.org/github.com/relvacode/grab)
[![Build Status](https://travis-ci.org/relvacode/grab.svg?branch=master)](https://travis-ci.org/relvacode/grab)

```go
import "github.com/relvacode/grab"
```

A simple client Go client for downloading files from `byte-range` supported HTTP servers (such as Amazon's S3 and compatible implementations). With automatic retry, seek and file verification built in.

It differs from other Go S3 clients by being simple and memory efficient. HTTP data is copied directly into the buffer used in a call to `Read`. Whilst concurrent ranged downloading is not supported you can copy many files in parallel with little memory overhead.

If a request fails, grab will transparently resume the connection from where it left off.

This library has transffered nearly a petabyte of data in production.
