package proxy

import (
	"net/http"
	"testing"
)

func TestCopyHeadersDropsBodyRepresentationHeaders(t *testing.T) {
	src := http.Header{}
	src.Set("Content-Type", "application/json")
	src.Set("Content-Encoding", "gzip")
	src.Set("Content-Length", "1234")
	src.Set("Content-MD5", "original-md5")
	src.Set("Digest", "sha-256=original")
	src.Set("Authorization", "Bearer client-token")

	dst := http.Header{}
	copyHeaders(dst, src)

	if got := dst.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type should be forwarded, got %q", got)
	}
	if got := dst.Get("Authorization"); got != "Bearer client-token" {
		t.Fatalf("Authorization should be forwarded for caller policy to handle, got %q", got)
	}
	for _, key := range []string{"Content-Encoding", "Content-Length", "Content-MD5", "Digest"} {
		if got := dst.Get(key); got != "" {
			t.Fatalf("%s should not be forwarded after request body rewrite, got %q", key, got)
		}
	}
}
