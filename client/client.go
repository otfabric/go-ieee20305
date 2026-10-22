// SPDX-License-Identifier: MIT

package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/security"
)

// MediaType is the media type of IEEE 2030.5 XML documents.
const MediaType = ieee20305.MediaType

// DefaultDeviceCapabilityPath is where a server serves its DeviceCapability
// resource unless discovery says otherwise.
const DefaultDeviceCapabilityPath = "/dcap"

const defaultMaxResponseBytes = 1 << 20

// Codec encodes and decodes documents: ieee20305.StandardCodec by default,
// csipaus.Codec for CSIP-AUS documents.
type Codec = ieee20305.Codec

// Config configures a Client.
type Config struct {
	// Server is the URL of the server, such as https://utility.example:8443.
	// The hrefs of resources are resolved against it, and the client does
	// not follow links or redirects to another scheme, host or port: its
	// device certificate identifies it to this server only. Required; the
	// scheme must be https.
	Server string
	// DeviceCapabilityPath is the path of the DeviceCapability resource;
	// DefaultDeviceCapabilityPath when empty.
	DeviceCapabilityPath string
	// HTTPClient makes the requests. Its transport holds the device
	// certificate and the CAs that verify the server: see NewHTTPClient.
	// Required.
	HTTPClient *http.Client
	// Codec encodes and decodes documents; package ieee20305 when nil.
	Codec Codec
	// UserAgent is sent with every request when not empty.
	UserAgent string
	// MaxResponseBytes limits the size of a response body; 1 MiB when zero.
	MaxResponseBytes int64
	// RetryPolicy decides whether a request that failed is sent again:
	// ieee20305.NoRetry() when nil; ieee20305.ExponentialBackoff{} retries
	// what may succeed (unavailable, 5xx, timeout) a few times. GET, HEAD,
	// PUT and DELETE are retried; POST only with RetryPOST.
	RetryPolicy ieee20305.RetryPolicy
	// RetryPOST lets RetryPolicy retry POSTs too. A POST whose answer is
	// lost may have taken effect: send it again only to a server that
	// stores it once (go-ieee20305's server does so for EndDevices,
	// Responses and mirrors).
	RetryPOST bool
	// Logger receives the client's log (see ieee20305.Logger): a Debug
	// line per request. None when nil.
	Logger ieee20305.Logger
	// MaxListItems limits the items Iterate and Walk read of one list, as
	// a list could claim to have any number: 100000 when zero.
	MaxListItems uint32
}

// Client talks to one IEEE 2030.5 server. It is safe for concurrent use.
type Client struct {
	server   *url.URL
	dcap     string
	http     *http.Client
	codec    Codec
	ua       string
	maxBytes int64
	maxItems uint32
	log      ieee20305.Logger
	retry    ieee20305.RetryPolicy
	// retryPOST is Config.RetryPOST.
	retryPOST bool
}

// New returns a Client for cfg.
func New(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.Server)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%w: Server must be an https URL without credentials, query or fragment, got %q", ieee20305.ErrConfiguration, cfg.Server)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	if cfg.HTTPClient == nil {
		return nil, fmt.Errorf("%w: HTTPClient is required: it carries the device certificate", ieee20305.ErrConfiguration)
	}
	c := &Client{server: u, dcap: cfg.DeviceCapabilityPath, codec: cfg.Codec, ua: cfg.UserAgent, maxBytes: cfg.MaxResponseBytes}
	if c.dcap == "" {
		c.dcap = DefaultDeviceCapabilityPath
	}
	if c.codec == nil {
		c.codec = ieee20305.StandardCodec
	}
	if c.maxBytes <= 0 {
		c.maxBytes = defaultMaxResponseBytes
	}
	c.log = ieee20305.ComponentLogger(cfg.Logger, "client")
	c.retry, c.retryPOST = cfg.RetryPolicy, cfg.RetryPOST
	if c.retry == nil {
		c.retry = ieee20305.NoRetry()
	}
	c.maxItems = cfg.MaxListItems
	if c.maxItems == 0 {
		c.maxItems = 100000
	}
	hc := *cfg.HTTPClient
	next := hc.CheckRedirect
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !c.sameOrigin(req.URL) {
			return fmt.Errorf("%w: redirect to another server: %s", ieee20305.ErrProtocol, req.URL.Redacted())
		}
		if next != nil {
			return next(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("%w: stopped after 10 redirects", ieee20305.ErrProtocol)
		}
		return nil
	}
	c.http = &hc
	return c, nil
}

// Codec returns the codec of the client's documents.
func (c *Client) Codec() Codec { return c.codec }

// NewHTTPClient returns an HTTP client for an IEEE 2030.5 client with the
// device certificate and the CAs that verify the server. It sets no
// timeout: bound each call with its context.
func NewHTTPClient(device tls.Certificate, roots *x509.CertPool) *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		TLSClientConfig:     security.ClientTLSConfig(device, roots),
		TLSHandshakeTimeout: 10 * time.Second,
	}}
}

func (c *Client) sameOrigin(u *url.URL) bool {
	return u.Scheme == c.server.Scheme && strings.EqualFold(u.Host, c.server.Host)
}

// Resolve returns the URL of href, which is relative to the server or an
// absolute URL of the server.
func (c *Client) Resolve(href string) (*url.URL, error) {
	ref, err := url.Parse(href)
	if err != nil {
		return nil, fmt.Errorf("%w: href %q: %v", ieee20305.ErrProtocol, href, err)
	}
	base := *c.server
	base.Path += "/"
	u := base.ResolveReference(ref)
	if !c.sameOrigin(u) {
		return nil, fmt.Errorf("%w: href %q is on another server", ieee20305.ErrProtocol, href)
	}
	return u, nil
}

// Get retrieves the resource at href into v, a pointer to a document type
// such as *ieee20305.EndDevice.
func (c *Client) Get(ctx context.Context, href string, v any) error {
	return c.get(ctx, href, nil, v)
}

// ListOptions select a page of a list resource.
type ListOptions struct {
	// Start is the index of the first item (the s query parameter).
	Start uint32
	// Limit is the maximum number of items (the l query parameter); the
	// server's default when zero.
	Limit uint32
	// SFDI selects the EndDevice with this SFDI in an EndDeviceList (the
	// sFDI query parameter); no selection when zero.
	SFDI uint64
}

// GetList retrieves a page of the list resource at href into v, a pointer
// to a list document type such as *ieee20305.EndDeviceList.
func (c *Client) GetList(ctx context.Context, href string, opts ListOptions, v any) error {
	q := url.Values{}
	q.Set("s", strconv.FormatUint(uint64(opts.Start), 10))
	if opts.Limit > 0 {
		q.Set("l", strconv.FormatUint(uint64(opts.Limit), 10))
	}
	if opts.SFDI != 0 {
		q.Set("sFDI", strconv.FormatUint(opts.SFDI, 10))
	}
	return c.get(ctx, href, q, v)
}

// Fetch retrieves the resource at href and returns a pointer to its
// document, of whatever type the server sent.
func (c *Client) Fetch(ctx context.Context, href string) (any, error) {
	data, err := c.body(ctx, href, nil)
	if err != nil {
		return nil, err
	}
	return c.codec.Decode(data)
}

// DeviceCapability retrieves the server's DeviceCapability resource, the
// root of its resources.
func (c *Client) DeviceCapability(ctx context.Context) (*ieee20305.DeviceCapability, error) {
	var dc ieee20305.DeviceCapability
	if err := c.Get(ctx, c.dcap, &dc); err != nil {
		return nil, err
	}
	return &dc, nil
}

// Post sends v to the list resource at href and returns the href of the
// resource the server created, from the Location header ("" if the server
// sent none).
func (c *Client) Post(ctx context.Context, href string, v any) (string, error) {
	resp, _, err := c.do(ctx, http.MethodPost, href, nil, v)
	if err != nil {
		return "", err
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", nil
	}
	u, err := c.Resolve(loc)
	if err != nil {
		return "", err
	}
	return u.RequestURI(), nil
}

// Put replaces the resource at href with v.
func (c *Client) Put(ctx context.Context, href string, v any) error {
	_, _, err := c.do(ctx, http.MethodPut, href, nil, v)
	return err
}

// Delete deletes the resource at href.
func (c *Client) Delete(ctx context.Context, href string) error {
	_, _, err := c.do(ctx, http.MethodDelete, href, nil, nil)
	return err
}

func (c *Client) get(ctx context.Context, href string, q url.Values, v any) error {
	data, err := c.body(ctx, href, q)
	if err != nil {
		return err
	}
	return c.codec.Unmarshal(data, v)
}

func (c *Client) body(ctx context.Context, href string, q url.Values) ([]byte, error) {
	resp, data, err := c.do(ctx, http.MethodGet, href, q, nil)
	if err != nil {
		return nil, err
	}
	if err := checkMediaType(resp.Header.Get("Content-Type")); err != nil {
		return nil, err
	}
	return data, nil
}

func checkMediaType(ct string) error {
	if ct == "" {
		return nil
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return fmt.Errorf("%w: Content-Type %q: %v", ieee20305.ErrProtocol, ct, err)
	}
	switch mt {
	case MediaType, "application/xml", "text/xml":
		return nil
	case "application/sep-exi":
		return fmt.Errorf("%w: the server answered with EXI, which this client does not decode", ieee20305.ErrProtocol)
	}
	return fmt.Errorf("%w: unexpected Content-Type %q", ieee20305.ErrProtocol, ct)
}

// do sends a request and returns its successful response with the body
// read, or the failure.
func (c *Client) do(ctx context.Context, method, href string, q url.Values, body any) (*http.Response, []byte, error) {
	u, err := c.Resolve(href)
	if err != nil {
		return nil, nil, err
	}
	if q != nil {
		merged := u.Query()
		for k, vs := range q {
			merged[k] = vs
		}
		u.RawQuery = merged.Encode()
	}
	var data []byte
	if body != nil {
		if data, err = c.codec.Marshal(body); err != nil {
			return nil, nil, fmt.Errorf("%w: %s %s: %w", ieee20305.ErrInvalid, method, u.Path, err)
		}
	}
	// A request is sent again only when it is safe to: a GET, HEAD, PUT
	// or DELETE, or a POST with RetryPOST.
	idempotent := method != http.MethodPost || c.retryPOST
	for attempt := 0; ; attempt++ {
		resp, out, err := c.send(ctx, method, u, data)
		if err == nil || !idempotent {
			return resp, out, err
		}
		retry, wait := c.retry.ShouldRetry(attempt, err)
		if !retry {
			return nil, nil, err
		}
		c.log.Debugf("%s %s: attempt %d failed, again in %v: %v", method, u.Path, attempt+1, wait, err)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, nil, transportError(ctx, method, u, ctx.Err())
		case <-t.C:
		}
	}
}

// send sends one request and reads its answer.
func (c *Client) send(ctx context.Context, method string, u *url.URL, data []byte) (*http.Response, []byte, error) {
	var reader io.Reader
	if data != nil {
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s %s: %w", ieee20305.ErrInvalid, method, u.Path, err)
	}
	req.Header.Set("Accept", MediaType)
	if data != nil {
		req.Header.Set("Content-Type", MediaType)
	}
	if c.ua != "" {
		req.Header.Set("User-Agent", c.ua)
	}
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		err = transportError(ctx, method, u, err)
		c.log.Debugf("%s %s: %v after %v", method, u.Path, err, time.Since(start))
		return nil, nil, err
	}
	c.log.Debugf("%s %s: %d in %v", method, u.Path, resp.StatusCode, time.Since(start))
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes+1))
	if err != nil {
		return nil, nil, transportError(ctx, method, u, err)
	}
	if int64(len(out)) > c.maxBytes {
		return nil, nil, fmt.Errorf("%w: %s %s: response larger than %d bytes", ieee20305.ErrProtocol, method, u.Path, c.maxBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		se := &ieee20305.StatusError{Method: method, Path: u.Path, StatusCode: resp.StatusCode,
			RetryAfter: retryAfter(resp.Header.Get("Retry-After"), time.Now())}
		if len(out) > 0 {
			if v, err := c.codec.Decode(out); err == nil {
				if doc, ok := v.(*ieee20305.Error); ok {
					se.Document = doc
				}
			}
		}
		return nil, nil, se
	}
	return resp, out, nil
}

// transportError classifies the failure of a request that got no answer,
// keeping err in the chain (errors.As finds a *tls.CertificateVerificationError,
// a *net.OpError, ...):
//
//   - a canceled context: ErrCanceled;
//   - a deadline or a network timeout: ErrTimeout;
//   - a certificate that does not verify, either way: ErrCertificate;
//   - a connection refused or reset, a host that does not resolve:
//     ErrUnavailable;
//   - a redirect or an answer the client refused: ErrProtocol.
func transportError(ctx context.Context, method string, u *url.URL, err error) error {
	wrap := func(kind error) error { return fmt.Errorf("%w: %s %s: %w", kind, method, u.Path, err) }
	var (
		ne        net.Error
		dnsErr    *net.DNSError
		opErr     *net.OpError
		verifyErr *tls.CertificateVerificationError
		alert     tls.AlertError
		unknownCA x509.UnknownAuthorityError
		invalid   x509.CertificateInvalidError
		hostname  x509.HostnameError
	)
	switch {
	case errors.Is(err, ieee20305.ErrProtocol), errors.Is(err, ieee20305.ErrCertificate):
		return err
	case errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled):
		return wrap(ieee20305.ErrCanceled)
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return wrap(ieee20305.ErrTimeout)
	case errors.As(err, &verifyErr), errors.As(err, &unknownCA), errors.As(err, &invalid), errors.As(err, &hostname),
		errors.As(err, &alert) && (alert == 42 || alert == 43 || alert == 44 || alert == 45 || alert == 46 || alert == 48 || alert == 116):
		// Alerts: bad_certificate, unsupported_certificate,
		// certificate_revoked, certificate_expired,
		// certificate_unknown, unknown_ca, certificate_required.
		return wrap(ieee20305.ErrCertificate)
	case errors.As(err, &opErr) && opErr.Op == "remote error" && remoteCertAlert(opErr.Err):
		// An alert the server sent: crypto/tls reports it as a
		// *net.OpError of an unexported type.
		return wrap(ieee20305.ErrCertificate)
	case errors.As(err, &dnsErr), errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.ECONNRESET),
		errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.As(err, &opErr):
		return wrap(ieee20305.ErrUnavailable)
	}
	return fmt.Errorf("ieee20305: %s %s: %w", method, u.Path, err)
}

// remoteCertAlert reports whether the alert a peer sent is about a
// certificate, by its text (the type crypto/tls gives it is unexported).
func remoteCertAlert(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "certificate") || strings.Contains(msg, "unknown ca")
}

// retryAfter parses a Retry-After header: seconds, or an HTTP date.
func retryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}
