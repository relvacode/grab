# Grab

[![GoDoc](https://godoc.org/github.com/relvacode/grab?status.svg)](https://godoc.org/github.com/relvacode/grab)

```go
import "github.com/relvacode/grab"
```

Grab is a simple request wrapper to support retryable downloads and seeking using byte range requests. It's it simpler than other ranged downloading libraries because it simply wraps the response body's reader and therefore doesn't do any large memory allocations
