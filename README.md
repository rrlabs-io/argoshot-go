# argoshot-go

The official Go client for [Argoshot](https://argoshot.com), the screenshot API: screenshots, PDFs and signed
`og:image` URLs of any web page or HTML. **Free to start.** Docs: https://argoshot.com/docs

Standard library only. Go 1.21+.

## Install

```sh
go get github.com/rrlabs-io/argoshot-go
```

Get your access key (and secret key, for signed URLs) from the dashboard's API keys page.

## Screenshot to a file

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/rrlabs-io/argoshot-go"
)

func main() {
	client := argoshot.New(os.Getenv("ARGOSHOT_ACCESS_KEY"))

	png, err := client.Take(context.Background(), argoshot.Options{
		"url":                  "https://example.com",
		"format":               "png",
		"full_page":            true,
		"block_cookie_banners": true,
	})
	if err != nil {
		log.Fatal(err)
	}
	os.WriteFile("example.png", png, 0o644)
}
```

## Signed og:image URL

`URL()` builds a `GET /take` link for `<img src>` or `<meta property="og:image">`. With a secret key it is signed
(HMAC-SHA256 of the query string), so nobody can change its options. Set the key to **signed requests only** in the
dashboard before you put its links in public pages.

```go
client := argoshot.New(os.Getenv("ARGOSHOT_ACCESS_KEY"), argoshot.WithSecretKey(os.Getenv("ARGOSHOT_SECRET_KEY")))

ogImage := client.URL(argoshot.Options{
	"url":             "https://example.com/blog/my-post",
	"viewport_width":  1200,
	"viewport_height": 630,
	"format":          "png",
	"cache":           true,
	"cache_ttl":       86400,
})
// <meta property="og:image" content="{{ .OGImage }}">
```

## HTML to PNG

When `html` (or `markdown`) is given, `Take()` sends a JSON `POST` instead of a long query string.

```go
card, err := client.Take(ctx, argoshot.Options{
	"html":            `<div style="font: 64px system-ui; padding: 80px">Hello from Argoshot</div>`,
	"format":          "png",
	"viewport_width":  1200,
	"viewport_height": 630,
})
if err != nil {
	log.Fatal(err)
}
os.WriteFile("card.png", card, 0o644)
```

## Options

`argoshot.Options` is a map of the API's own option names (https://argoshot.com/docs/options): `url`, `html`,
`format`, `viewport_width`, `viewport_height`, `viewport_device`, `full_page`, `block_cookie_banners`, `block_ads`,
`dark_mode`, `selector`, `cache`, `cache_ttl` and 140 more. Lists are slices: `"hide_selectors": []string{".banner", "#chat"}`.

## Errors

A failure the API answers is an `*argoshot.Error` with the API's `Code` (e.g. `selector_not_found`,
`screenshots_limit_reached`), `Message`, `StatusCode`, `DocumentationURL`, and `UpgradeURL` / `RetryAfter` when the API
gives them. All codes: https://argoshot.com/docs/errors

```go
var e *argoshot.Error
if errors.As(err, &e) {
	log.Printf("%s: %s (HTTP %d)", e.Code, e.Message, e.StatusCode)
}
if argoshot.IsCode(err, "concurrency_limit_reached") {
	// back off and retry
}
```

## Usage

```go
u, err := client.Usage(ctx) // u.Available, u.Used, u.Total ...
```

## Configuration

```go
argoshot.New(accessKey,
	argoshot.WithSecretKey("..."),                    // sign URL() and GET Take() requests
	argoshot.WithBaseURL("https://api.argoshot.com"), // default
	argoshot.WithHTTPClient(&http.Client{Timeout: 2 * time.Minute}),
)
```

Note: a key set to *signed requests only* cannot send JSON `POST` requests (the API refuses them), so `Take()` with
`html` needs a key that permits unsigned requests.

## Licence

MIT
