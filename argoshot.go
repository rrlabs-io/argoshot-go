// Package argoshot is a small client for the Argoshot screenshot API (https://argoshot.com/docs).
//
//	client := argoshot.New("ACCESS_KEY", argoshot.WithSecretKey("SECRET_KEY"))
//	png, err := client.Take(ctx, argoshot.Options{"url": "https://example.com", "format": "png"})
//	ogImage := client.URL(argoshot.Options{"url": "https://example.com", "viewport_width": 1200, "viewport_height": 630})
//
// Options are the API's own names (https://argoshot.com/docs/options). Standard library only.
package argoshot

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Version is this client's version.
const Version = "0.1.0"

// DefaultBaseURL is the API's address.
const DefaultBaseURL = "https://api.argoshot.com"

// Options are a request's options by the API's names: "url", "html", "format", "viewport_width", "full_page",
// "block_cookie_banners", "cache_ttl" ... Lists ("hide_selectors", "block_requests") are slices. Nil values are left out.
type Options map[string]any

// Client calls the Argoshot API. Its zero value is not usable: make one with New.
type Client struct {
	AccessKey  string
	SecretKey  string // when set, URL and GET Take requests are signed
	BaseURL    string
	HTTPClient *http.Client
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithSecretKey signs URL() links and GET Take requests with this secret key.
func WithSecretKey(secretKey string) ClientOption { return func(c *Client) { c.SecretKey = secretKey } }

// WithBaseURL sets the API's address (default https://api.argoshot.com).
func WithBaseURL(baseURL string) ClientOption {
	return func(c *Client) { c.BaseURL = strings.TrimRight(baseURL, "/") }
}

// WithHTTPClient sets the http.Client requests use (default: one with a 120 second timeout).
func WithHTTPClient(h *http.Client) ClientOption { return func(c *Client) { c.HTTPClient = h } }

// New makes a Client with your access key.
func New(accessKey string, opts ...ClientOption) *Client {
	c := &Client{AccessKey: accessKey, BaseURL: DefaultBaseURL, HTTPClient: &http.Client{Timeout: 120 * time.Second}}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Error is an error the API answered (its error_code and error_message), or a response that was not the API's
// (Code "http_error").
type Error struct {
	Code               string
	Message            string
	StatusCode         int
	DocumentationURL   string
	UpgradeURL         string
	ReturnedStatusCode int
	MatchedString      string
	RetryAfter         int // seconds, from Retry-After (0: none)
	Body               string
}

func (e *Error) Error() string { return "argoshot: " + e.Code + ": " + e.Message }

// URL is a GET /take link for these options, for <img src> or og:image, signed when a secret key is set.
// The access key is in the link: for public pages, use a key set to "signed requests only".
func (c *Client) URL(o Options) string {
	query := BuildQuery(o)
	if query != "" {
		query += "&"
	}
	query += "access_key=" + encode(c.AccessKey)
	if c.SecretKey != "" {
		query += "&signature=" + Sign(c.SecretKey, query)
	}
	return c.base() + "/take?" + query
}

// Take renders and returns the file's bytes. GET for url captures (signed when a secret key is set), a JSON POST
// when "html" or "markdown" is given. A failure the API answers is an *Error.
func (c *Client) Take(ctx context.Context, o Options) ([]byte, error) {
	opts := clean(o)
	var req *http.Request
	var err error
	_, hasHTML := opts["html"]
	_, hasMarkdown := opts["markdown"]
	if hasHTML || hasMarkdown {
		body, jerr := json.Marshal(opts)
		if jerr != nil {
			return nil, fmt.Errorf("argoshot: options are not JSON: %w", jerr)
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/take", bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	} else {
		query := BuildQuery(opts)
		if c.SecretKey != "" {
			query += "&signature=" + Sign(c.SecretKey, query)
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/take?"+query, nil)
	}
	if err != nil {
		return nil, err
	}
	return c.send(req)
}

// Usage is GET /usage's answer.
type Usage struct {
	Total       int  `json:"total"`
	Available   int  `json:"available"`
	Used        int  `json:"used"`
	Lifetime    bool `json:"lifetime"`
	Concurrency struct {
		Limit int `json:"limit"`
	} `json:"concurrency"`
	Rate struct {
		PerMinute int `json:"per_minute"`
	} `json:"rate"`
	PeriodEnd int64 `json:"period_end"`
}

// Usage returns the renders total, available and used, and the plan's limits. Costs nothing.
func (c *Client) Usage(ctx context.Context) (*Usage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/usage", nil)
	if err != nil {
		return nil, err
	}
	body, err := c.send(req)
	if err != nil {
		return nil, err
	}
	var u Usage
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("argoshot: /usage answered %q: %w", truncate(string(body)), err)
	}
	return &u, nil
}

func (c *Client) send(req *http.Request) ([]byte, error) {
	req.Header.Set("X-Access-Key", c.AccessKey)
	req.Header.Set("User-Agent", "argoshot-go/"+Version)
	h := c.HTTPClient
	if h == nil {
		h = http.DefaultClient
	}
	resp, err := h.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, ParseError(resp.StatusCode, body, resp.Header.Get("Retry-After"))
	}
	return body, nil
}

func (c *Client) base() string {
	if c.BaseURL == "" {
		return DefaultBaseURL
	}
	return strings.TrimRight(c.BaseURL, "/")
}

// ParseError makes an *Error from an error response's status, body and Retry-After header.
func ParseError(status int, body []byte, retryAfter string) *Error {
	retry, _ := strconv.Atoi(strings.TrimSpace(retryAfter))
	var data struct {
		ErrorCode          *string `json:"error_code"`
		ErrorMessage       string  `json:"error_message"`
		DocumentationURL   string  `json:"documentation_url"`
		UpgradeURL         string  `json:"upgrade_url"`
		ReturnedStatusCode int     `json:"returned_status_code"`
		MatchedString      string  `json:"matched_string"`
	}
	if json.Unmarshal(body, &data) == nil && data.ErrorCode != nil {
		message := data.ErrorMessage
		if message == "" {
			message = *data.ErrorCode
		}
		return &Error{Code: *data.ErrorCode, Message: message, StatusCode: status, DocumentationURL: data.DocumentationURL,
			UpgradeURL: data.UpgradeURL, ReturnedStatusCode: data.ReturnedStatusCode, MatchedString: data.MatchedString,
			RetryAfter: retry, Body: string(body)}
	}
	message := fmt.Sprintf("HTTP %d", status)
	if len(body) > 0 {
		message += ": " + truncate(string(body))
	}
	return &Error{Code: "http_error", Message: message, StatusCode: status, RetryAfter: retry, Body: string(body)}
}

// IsCode reports whether err is an *Error with this API error code (e.g. "screenshots_limit_reached").
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// BuildQuery is the query string of these options: names sorted, lists repeated, nil left out (no access key).
func BuildQuery(o Options) string {
	opts := clean(o)
	names := make([]string, 0, len(opts))
	for name := range opts {
		names = append(names, name)
	}
	sort.Strings(names)
	var parts []string
	for _, name := range names {
		for _, v := range values(opts[name]) {
			parts = append(parts, encode(name)+"="+encode(v))
		}
	}
	return strings.Join(parts, "&")
}

// Sign is the signature the API checks: lowercase hex HMAC-SHA256 of the query string, under the secret key.
func Sign(secretKey, query string) string {
	m := hmac.New(sha256.New, []byte(secretKey))
	m.Write([]byte(query))
	return hex.EncodeToString(m.Sum(nil))
}

func clean(o Options) Options {
	out := Options{}
	for k, v := range o {
		if v == nil || k == "access_key" || k == "signature" {
			continue
		}
		rv := reflect.ValueOf(v)
		if (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Slice || rv.Kind() == reflect.Map) && rv.IsNil() {
			continue
		}
		out[k] = v
	}
	return out
}

func values(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case string, []byte:
		return []string{text(x)}
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		out := make([]string, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			e := rv.Index(i).Interface()
			if e != nil {
				out = append(out, text(e))
			}
		}
		return out
	}
	return []string{text(v)}
}

func text(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case fmt.Stringer:
		return x.String()
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer && !rv.IsNil() {
		return text(rv.Elem().Interface())
	}
	return fmt.Sprint(v)
}

// encode is RFC 3986: everything but A-Z a-z 0-9 - . _ ~ is percent-encoded (a space is %20, a + is %2B).
func encode(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&15])
		}
	}
	return b.String()
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
