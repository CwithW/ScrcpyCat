package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSPAAndStaticResources(t *testing.T) {
	web := handler(fstest.MapFS{
		"index.html":                   {Data: []byte("<!doctype html><title>ScrcpyCat</title>")},
		"assets/worker-12345678.js":    {Data: []byte("postMessage('ready')")},
		"assets/decoder-12345678.wasm": {Data: []byte("\x00asm\x01\x00\x00\x00")},
		".keep":                        {Data: nil},
	})
	for _, route := range []string{"/", "/index.html", "/deploy", "/terminal", "/share?token=fixture"} {
		rec := httptest.NewRecorder()
		web.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, route, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ScrcpyCat") || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("SPA %s: %d %s", route, rec.Code, rec.Body.String())
		}
	}
	for _, route := range []string{"/api/missing", "/api", "/devices/unknown", "/snapshots/missing", "/assets/missing.js", "/assets/missing", "/missing.wasm", "/.keep", "/.git/config"} {
		rec := httptest.NewRecorder()
		web.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, route, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("missing route %s returned %d", route, rec.Code)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/assets/decoder-12345678.wasm", nil)
	rec := httptest.NewRecorder()
	web.ServeHTTP(rec, request)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/wasm" || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("WASM response: %d %v", rec.Code, rec.Header())
	}
	request.Header.Set("If-None-Match", rec.Header().Get("ETag"))
	rec = httptest.NewRecorder()
	web.ServeHTTP(rec, request)
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Fatal("conditional static request did not return 304")
	}
	request.Header.Del("If-None-Match")
	request.Header.Set("Range", "bytes=0-3")
	rec = httptest.NewRecorder()
	web.ServeHTTP(rec, request)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "\x00asm" {
		t.Fatal("static range request failed")
	}
	for _, method := range []string{http.MethodHead, http.MethodPost} {
		rec = httptest.NewRecorder()
		web.ServeHTTP(rec, httptest.NewRequest(method, "/deploy", nil))
		if method == http.MethodHead && (rec.Code != http.StatusOK || rec.Body.Len() != 0) {
			t.Fatal("HEAD returned a body or failed")
		}
		if method == http.MethodPost && rec.Code != http.StatusMethodNotAllowed {
			t.Fatal("static handler accepted POST")
		}
	}
}

func TestMissingBuildIsExplicit(t *testing.T) {
	rec := httptest.NewRecorder()
	handler(fstest.MapFS{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing build returned %d", rec.Code)
	}
}
