package proxy

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// modelRefSuffix is the value suffix that signals credential injection.
// A Bearer token "api-key:ref" means: look up "api-key" in the real secret mount.
const modelRefSuffix = ":ref"

// modelAPIKeySuffix and modelURLSuffix are the key-naming conventions for multi-model secrets.
const (
	modelAPIKeySuffix = "_api-key"
	modelURLSuffix    = "_url"
)

// xModelCredError is an internal sentinel header set by the Director when ref resolution fails.
// The modelRoundTripper checks for it and returns 400 to the eval container instead of
// forwarding a request with a literal ref token.
const xModelCredError = "X-Model-Cred-Error"

// NewModelReverseProxy returns an httputil.ReverseProxy that performs model credential injection.
//
// Per-request behaviour:
//  1. If the Authorization header carries a ref token (e.g. "Bearer model-1_api-key:ref"):
//     a. The real credential is read from secretMountPath/model-1_api-key.
//     b. The upstream URL is determined by reading secretMountPath/model-1_url
//     (derived by replacing "_api-key" → "_url"). Falls back to defaultTarget when
//     the URL file is absent (single-model / hf-token case).
//  2. If resolution fails (missing file, empty value, path traversal) the proxy returns
//     HTTP 400 to the eval container — the request is never forwarded.
//  3. Non-ref tokens are forwarded unchanged to defaultTarget.
func NewModelReverseProxy(defaultTarget *url.URL, client *http.Client, logger *slog.Logger, secretMountPath string) *httputil.ReverseProxy {
	rp := &httputil.ReverseProxy{
		Transport: &modelRoundTripper{
			inner:  &roundTripperFromClient{client: client},
			logger: logger,
		},
	}

	rp.Director = func(req *http.Request) {
		target := defaultTarget

		authHeader := req.Header.Get("Authorization")
		if isModelRefToken(authHeader) {
			resolvedTarget, realToken, err := resolveModelCredential(logger, authHeader, secretMountPath, defaultTarget)
			if err != nil {
				// Signal the RoundTripper to return 400 without forwarding.
				req.Header.Set(xModelCredError, err.Error())
				req.URL.Scheme = defaultTarget.Scheme
				req.URL.Host = defaultTarget.Host
				req.Host = defaultTarget.Host
				req.RequestURI = ""
				return
			}
			target = resolvedTarget
			SetAuthHeader(req, realToken)
		}

		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host
		req.RequestURI = ""
		logger.Info("Proxying model request", "method", req.Method, "url", req.URL.String(), "headers", headersForLog(req.Header))
	}

	rp.ModifyResponse = func(resp *http.Response) error {
		if resp.Request != nil {
			logger.Info("Response from model proxy", "method", resp.Request.Method, "url", resp.Request.URL.String(), "status", resp.StatusCode)
		}
		return nil
	}

	rp.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
		logger.Error("Error proxying model request", "method", req.Method, "url", req.URL.String(), "error", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
	}

	return rp
}

// modelRoundTripper wraps an inner RoundTripper and intercepts requests marked with the
// xModelCredError sentinel header, returning 400 Bad Request without forwarding.
type modelRoundTripper struct {
	inner  http.RoundTripper
	logger *slog.Logger
}

func (t *modelRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if errMsg := req.Header.Get(xModelCredError); errMsg != "" {
		req.Header.Del(xModelCredError)
		t.logger.Error("model credential resolution failed, returning 400", "error", errMsg)
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Status:     "400 Bad Request",
			Body:       io.NopCloser(strings.NewReader(errMsg + "\n")),
			Header:     make(http.Header),
			Request:    req,
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
		}, nil
	}
	return t.inner.RoundTrip(req)
}

// isModelRefToken reports whether authHeader is a Bearer ref token.
func isModelRefToken(authHeader string) bool {
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return false
	}
	return strings.HasSuffix(strings.TrimPrefix(authHeader, "Bearer "), modelRefSuffix)
}

// resolveModelCredential resolves a Bearer ref token to a (upstream URL, real credential) pair.
//
// Key derivation for the upstream URL:
//   - "model-1_api-key:ref" → reads secretMountPath/model-1_api-key (token) and
//     secretMountPath/model-1_url (URL); falls back to defaultTarget if _url absent.
//   - "api-key:ref" / "hf-token:ref" → reads the credential file; always uses defaultTarget.
func resolveModelCredential(logger *slog.Logger, authHeader, secretMountPath string, defaultTarget *url.URL) (*url.URL, string, error) {
	token := strings.TrimPrefix(authHeader, "Bearer ")
	key := strings.TrimSuffix(token, modelRefSuffix)

	if key == "" || strings.ContainsAny(key, "/\\") {
		return nil, "", fmt.Errorf("model ref token has invalid key %q", key)
	}

	realToken, err := readSecretFile(secretMountPath, key)
	if err != nil {
		logger.Error("failed to read model credential", "key", key)
		return nil, "", fmt.Errorf("credential not found for key %q", key)
	}
	if realToken == "" {
		return nil, "", fmt.Errorf("credential for key %q is empty", key)
	}

	target := defaultTarget

	// For multi-model keys (*_api-key), try to read the corresponding URL file.
	if strings.HasSuffix(key, modelAPIKeySuffix) {
		prefix := strings.TrimSuffix(key, modelAPIKeySuffix)
		urlKey := prefix + modelURLSuffix
		rawURL, urlErr := readSecretFile(secretMountPath, urlKey)
		if urlErr == nil && rawURL != "" {
			parsed, parseErr := url.Parse(strings.TrimSuffix(rawURL, "/"))
			if parseErr != nil {
				logger.Warn("model URL file has invalid URL, using default target",
					"url_key", urlKey, "error", parseErr)
			} else {
				target = parsed
			}
		}
	}

	logger.Info("Resolved model ref token", "key", key, "target_host", target.Host)
	return target, realToken, nil
}

// readSecretFile reads and trims a single file from a Kubernetes secret mount.
func readSecretFile(mountPath, key string) (string, error) {
	data, err := os.ReadFile(filepath.Join(mountPath, key))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
