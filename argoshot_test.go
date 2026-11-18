package argoshot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// The server's own test (jai-web-framework framework/Screenshots/tests/privacy/app.jai:194):
// hmac_sha256("secret-of-the-customer-1234", "html=x&access_key=key-<tenant>"), lowercase hex.
func TestServerVector(t *testing.T) {
	c := New("key-2", WithSecretKey("secret-of-the-customer-1234"))
	want := "https://api.argoshot.com/take?html=x&access_key=key-2&signature=9488e5f178e81282b921f410775de15b0767f894504cd18bd66f35616d60ffd1"
	if got := c.URL(Options{"html": "x"}); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestSignedURL(t *testing.T) {
	c := New("AK_test", WithSecretKey("SK_test"))
	got := c.URL(Options{
		"url":                 "https://example.com/a b?x=1&y=2",
		"format":              "png",
		"full_page":           true,
		"viewport_width":      1280,
		"device_scale_factor": 1.5,
		"hide_selectors":      []string{".a", "#b .c"},
	})
	want := "https://api.argoshot.com/take?device_scale_factor=1.5&format=png&full_page=true&hide_selectors=.a&hide_selectors=%23b%20.c&url=https%3A%2F%2Fexample.com%2Fa%20b%3Fx%3D1%26y%3D2&viewport_width=1280&access_key=AK_test&signature=1a6f9d69eae82c57575f031ba76325705f5c0ddc6b48380a6fa7964c8a497b1c"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestNonASCIIHTML(t *testing.T) {
	c := New("AK_test", WithSecretKey("SK_test"))
	got := c.URL(Options{"html": "<h1>Hi — café + &</h1>", "format": "png"})
	want := "https://api.argoshot.com/take?format=png&html=%3Ch1%3EHi%20%E2%80%94%20caf%C3%A9%20%2B%20%26%3C%2Fh1%3E&access_key=AK_test&signature=a1fb4eea53e2c394dce7ab32e31a26a2c8c85b8ebc53ac330ddb0cc441ee9eb7"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestUnsignedCustomBaseNilSkipped(t *testing.T) {
	c := New("AK_test", WithBaseURL("http://localhost:8080/"))
	var none []string
	got := c.URL(Options{"url": "https://example.com", "format": "png", "selector": nil, "hide_selectors": none, "access_key": "other", "signature": "x"})
	want := "http://localhost:8080/take?format=png&url=https%3A%2F%2Fexample.com&access_key=AK_test"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestReservedCharactersAndAnyList(t *testing.T) {
	if got := BuildQuery(Options{"styles": "a{content:'!()*'}"}); got != "styles=a%7Bcontent%3A%27%21%28%29%2A%27%7D" {
		t.Fatal(got)
	}
	if got := BuildQuery(Options{"wait_until": []any{"load", "networkidle0"}}); got != "wait_until=load&wait_until=networkidle0" {
		t.Fatal(got)
	}
}

func TestParseError(t *testing.T) {
	e := ParseError(400, []byte(`{"is_successful":false,"error_code":"selector_not_found","error_message":"Nothing matches the selector: #hero","documentation_url":"https://argoshot.com/docs/errors/selector-not-found/"}`), "")
	if e.Code != "selector_not_found" || e.Message != "Nothing matches the selector: #hero" || e.StatusCode != 400 || e.DocumentationURL != "https://argoshot.com/docs/errors/selector-not-found/" {
		t.Fatalf("%+v", e)
	}
	e = ParseError(402, []byte(`{"is_successful":false,"error_code":"free_tier_exhausted","error_message":"Used.","upgrade_url":"https://argoshot.com/pricing"}`), "5")
	if e.Code != "free_tier_exhausted" || e.UpgradeURL != "https://argoshot.com/pricing" || e.RetryAfter != 5 {
		t.Fatalf("%+v", e)
	}
	e = ParseError(502, []byte("<html>Bad gateway</html>"), "")
	if e.Code != "http_error" || e.StatusCode != 502 || !strings.Contains(e.Message, "HTTP 502") {
		t.Fatalf("%+v", e)
	}
}

func TestTakeGetSigned(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("\x89PNG"))
	}))
	defer srv.Close()
	c := New("AK_test", WithSecretKey("SK_test"), WithBaseURL(srv.URL))
	body, err := c.Take(context.Background(), Options{"url": "https://example.com", "format": "png"})
	if err != nil || string(body) != "\x89PNG" {
		t.Fatal(err, body)
	}
	query := "format=png&url=https%3A%2F%2Fexample.com"
	if got.Method != "GET" || got.URL.RawQuery != query+"&signature="+Sign("SK_test", query) || got.Header.Get("X-Access-Key") != "AK_test" {
		t.Fatalf("%s %s %v", got.Method, got.URL.RawQuery, got.Header)
	}
}

func TestTakePostHTML(t *testing.T) {
	var method, contentType string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, contentType = r.Method, r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := New("AK_test", WithBaseURL(srv.URL))
	if _, err := c.Take(context.Background(), Options{"html": "<h1>Hi</h1>", "format": "png", "full_page": true}); err != nil {
		t.Fatal(err)
	}
	if method != "POST" || contentType != "application/json" || body["html"] != "<h1>Hi</h1>" || body["full_page"] != true {
		t.Fatalf("%s %s %v", method, contentType, body)
	}
}

func TestTakeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		w.Write([]byte(`{"is_successful":false,"error_code":"access_key_invalid","error_message":"No such access key."}`))
	}))
	defer srv.Close()
	_, err := New("bad", WithBaseURL(srv.URL)).Take(context.Background(), Options{"url": "https://example.com"})
	var e *Error
	if !errors.As(err, &e) || e.Code != "access_key_invalid" || e.StatusCode != 400 || !IsCode(err, "access_key_invalid") {
		t.Fatalf("%v", err)
	}
}

func TestLive(t *testing.T) {
	key := os.Getenv("ARGOSHOT_KEY")
	if key == "" {
		t.Skip("set ARGOSHOT_KEY to run the live test")
	}
	c := New(key, WithSecretKey(os.Getenv("ARGOSHOT_SECRET")))
	body, err := c.Take(context.Background(), Options{"url": "https://example.com", "format": "png"})
	if err != nil || len(body) < 4 || string(body[:4]) != "\x89PNG" {
		t.Fatal(err)
	}
}
