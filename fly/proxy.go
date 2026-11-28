// Path-based reverse proxy that sits in front of the Next.js web app
// and the shared agent terminal on a single Fly machine.
//
//	/terminal and /terminal/*  -> agentterm (prefix stripped)
//	agentterm API paths        -> agentterm (so the UI's /api/* calls work)
//	everything else            -> Next.js
package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

var termAPI = map[string]bool{
	"/api/info":     true,
	"/api/state":    true,
	"/api/log":      true,
	"/api/verify":   true,
	"/api/stream":   true,
	"/api/clock-in": true,
	"/api/entries":  true,
}

func proxy(target *url.URL) *httputil.ReverseProxy {
	p := httputil.NewSingleHostReverseProxy(target)
	p.FlushInterval = -1
	// Add the security headers after the upstream answers, and only those it
	// did not set itself, so no header is ever sent twice.
	p.ModifyResponse = func(resp *http.Response) error {
		addSecurityHeaders(resp.Header)
		return nil
	}
	p.Transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		// 0: agentterm's /api/stream is a long-lived SSE response.
		ResponseHeaderTimeout: 0,
	}
	return p
}

// securityHeaders are added to every response that does not already set
// them. The Next.js app adds its own Content-Security-Policy; agentterm sets
// a stricter one for its UI.
var securityHeaders = map[string]string{
	"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	"X-Content-Type-Options":    "nosniff",
	"X-Frame-Options":           "DENY",
	"Referrer-Policy":           "strict-origin-when-cross-origin",
	"Permissions-Policy":        "camera=(), microphone=(), geolocation=(), payment=()",
}

// addSecurityHeaders sets each security header the response does not
// already carry.
func addSecurityHeaders(h http.Header) {
	for k, v := range securityHeaders {
		if h.Get(k) == "" {
			h.Set(k, v)
		}
	}
}

// newHandler routes /terminal and the agentterm API paths to term and
// everything else to web. Requests that reached Fly's edge over plain HTTP
// (X-Forwarded-Proto: http) are redirected to https; Fly's own health checks
// talk to the machine directly, and /api/health is never redirected so a
// check can not fail on a redirect.
func newHandler(web, term http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "http") && r.Host != "" && r.URL.Path != "/api/health" {
			target := "https://" + r.Host + r.URL.RequestURI()
			w.Header().Set("Location", target)
			w.WriteHeader(http.StatusPermanentRedirect)
			return
		}
		path := r.URL.Path
		switch {
		case path == "/terminal" || path == "/terminal/" || strings.HasPrefix(path, "/terminal/"):
			r.URL.Path = strings.TrimPrefix(path, "/terminal")
			if r.URL.Path == "" {
				r.URL.Path = "/"
			}
			r.URL.RawPath = ""
			term.ServeHTTP(w, r)
		case termAPI[path]:
			term.ServeHTTP(w, r)
		default:
			web.ServeHTTP(w, r)
		}
	})
}

func main() {
	listen := flag.String("listen", "0.0.0.0:8080", "proxy listen address")
	webAddr := flag.String("web", "127.0.0.1:3000", "Next.js listen address")
	termAddr := flag.String("term", "127.0.0.1:8787", "agentterm listen address")
	flag.Parse()

	webURL, err := url.Parse("http://" + *webAddr)
	if err != nil {
		log.Fatalf("web url: %v", err)
	}
	termURL, err := url.Parse("http://" + *termAddr)
	if err != nil {
		log.Fatalf("term url: %v", err)
	}
	webP, termP := proxy(webURL), proxy(termURL)

	handler := newHandler(webP, termP)

	srv := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("flyproxy listening on %s (web=%s term=%s)", *listen, *webAddr, *termAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
