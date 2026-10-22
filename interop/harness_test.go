// SPDX-License-Identifier: MIT

package interop

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/tlsccm"
)

// image returns the image to run: the environment variable env, or def.
func image(env, def string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return def
}

func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("the interop suite needs Docker")
	}
	if _, err := docker(context.Background(), "info", "--format", "{{.ServerVersion}}"); err != nil {
		t.Skipf("the interop suite needs Docker: %v", err)
	}
}

func docker(ctx context.Context, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// The cipher suites of the contract's capabilities, by IANA name.
const (
	suiteCCM8   = "TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8"
	suiteGCM128 = "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256"
)

// capabilities is the part of print-capabilities the suite reads.
type capabilities struct {
	Adapter  string `json:"adapter"`
	Upstream struct {
		Version string `json:"version"`
	} `json:"upstream"`
	TLS struct {
		CipherSuites []string `json:"cipherSuites"`
	} `json:"tls"`
	Versions map[string]struct {
		Client role `json:"client"`
		Server role `json:"server"`
	} `json:"versions"`
}

type role struct {
	Supported  bool     `json:"supported"`
	Operations []string `json:"operations"`
	Fixtures   []string `json:"fixtures"`
	Calls      []string `json:"calls"`
}

func capabilitiesOf(t *testing.T, img string) capabilities {
	t.Helper()
	out, err := docker(context.Background(), "run", "--rm", img, "print-capabilities")
	if err != nil {
		t.Fatal(err)
	}
	var c capabilities
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatalf("capabilities of %s: %v", img, err)
	}
	return c
}

func (c capabilities) offers(suite string) bool { return slices.Contains(c.TLS.CipherSuites, suite) }

// device is a device certificate and its key.
type device struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func (d device) tlsCertificate() tls.Certificate {
	return tls.Certificate{Certificate: [][]byte{d.cert.Raw}, PrivateKey: d.key, Leaf: d.cert}
}

// pki is the TLS material of a run, laid out as the --tls-dir directories of
// the ieee20305-interop container contract.
type pki struct {
	dir     string // the directory to mount at /tls
	roots   *x509.CertPool
	server  tls.Certificate
	device  device // provisioned on the server, at client/
	other   device // signed by the same CA, not provisioned
	foreign device // signed by another CA
}

// hmn is the HardwareModuleName of a device of the suite.
func hmn(serial string) security.HMN {
	return security.HMN{Type: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 2030, 5}, SerialNumber: []byte(serial)}
}

// contractCA returns a CA as the contract's make_pki makes one: critical
// basic constraints and key usage, and a subject key identifier.
func contractCA(t *testing.T, name string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(48 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

// contractServer returns a server certificate as the contract's make_pki
// makes one: for the host names and IP addresses names, with the key usages
// digitalSignature and keyAgreement and the extended key usages serverAuth
// and clientAuth. Its subject is empty, as in IEEE 2030.5 certificates, so
// that its subjectAltName is critical, which the EPRI client requires.
func contractServer(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, names []string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(48 * time.Hour),
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyAgreement,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

// newPKI writes a CA, a server certificate for the names, the device of
// serial (whose certificate names its hardware module) in server/devices
// and client/, and two more devices.
func newPKI(t *testing.T, serial string, names ...string) *pki {
	t.Helper()
	ca, caKey := contractCA(t, "ieee20305 interop test CA")
	serverCert, serverKey := contractServer(t, ca, caKey, names)
	newDevice := func(ca *x509.Certificate, key *ecdsa.PrivateKey, serial string) device {
		cert, k, err := security.TestDeviceCertHMN(ca, key, hmn(serial))
		if err != nil {
			t.Fatal(err)
		}
		return device{cert, k}
	}
	otherCA, otherKey := contractCA(t, "another CA")
	p := &pki{
		dir:     t.TempDir(),
		roots:   x509.NewCertPool(),
		server:  tls.Certificate{Certificate: [][]byte{serverCert.Raw}, PrivateKey: serverKey, Leaf: serverCert},
		device:  newDevice(ca, caKey, serial),
		other:   newDevice(ca, caKey, "unprovisioned-"+serial),
		foreign: newDevice(otherCA, otherKey, serial),
	}
	p.roots.AddCert(ca)
	server := filepath.Join(p.dir, "server")
	writePEM(t, filepath.Join(server, "server.crt"), "CERTIFICATE", serverCert.Raw)
	writeKey(t, filepath.Join(server, "server.key"), serverKey)
	writePEM(t, filepath.Join(server, "ca.crt"), "CERTIFICATE", ca.Raw)
	writePEM(t, filepath.Join(server, "devices", serial+".crt"), "CERTIFICATE", p.device.cert.Raw)
	cl := filepath.Join(p.dir, "client")
	writePEM(t, filepath.Join(cl, "client.crt"), "CERTIFICATE", p.device.cert.Raw)
	writeKey(t, filepath.Join(cl, "client.key"), p.device.key)
	writePEM(t, filepath.Join(cl, "ca.crt"), "CERTIFICATE", ca.Raw)
	// The certificate of the client's notification listener, which the
	// suite publishes on the loopback address.
	notifyCert, notifyKey := contractServer(t, ca, caKey, []string{"127.0.0.1", "localhost"})
	writePEM(t, filepath.Join(cl, "notify.crt"), "CERTIFICATE", notifyCert.Raw)
	writeKey(t, filepath.Join(cl, "notify.key"), notifyKey)
	// The containers run as another user: the throwaway material must be
	// readable by it.
	err := filepath.Walk(p.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if info.IsDir() {
			mode = 0o755
		}
		return os.Chmod(path, mode)
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func writePEM(t *testing.T, path, kind string, der []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeKey(t *testing.T, path string, key *ecdsa.PrivateKey) {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	writePEM(t, path, "PRIVATE KEY", der)
}

// serverContainer is a running ieee20305-interop server container.
type serverContainer struct {
	id  string
	url string // https://127.0.0.1:<port>
}

// startServer runs img as a server of IEEE 2030.5 version, seeded with its
// baked fixture, with the TLS material of p, and waits until it is ready.
func startServer(t *testing.T, img, version, fixture string, p *pki) *serverContainer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := docker(ctx, "run", "--detach", "--publish", "127.0.0.1::443",
		"--add-host", "host.docker.internal:host-gateway", "--volume", p.dir+":/tls:ro",
		img, "server", "--version", version, "--tls-dir", "/tls/server", "--fixture", fixturePath(fixture))
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(out)
	t.Cleanup(func() {
		if t.Failed() {
			logs, _ := docker(context.Background(), "logs", "--tail", "60", id)
			t.Logf("server events:\n%s", logs)
		}
		_, _ = docker(context.Background(), "rm", "--force", id)
	})
	for {
		if state, _ := docker(ctx, "inspect", "--format", "{{.State.Status}}", id); strings.TrimSpace(state) == "exited" {
			t.Fatalf("the server exited before it was ready")
		}
		if _, err := docker(ctx, "exec", id, "test", "-f", "/run/ieee20305-interop/ready"); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("the server was not ready within 5 minutes")
		case <-time.After(time.Second):
		}
	}
	port, err := docker(ctx, "port", id, "443/tcp")
	if err != nil {
		t.Fatal(err)
	}
	addr := strings.TrimSpace(strings.Split(strings.TrimSpace(port), "\n")[0])
	return &serverContainer{id: id, url: "https://" + addr}
}

// transport is how the client of this module connects: crypto/tls (GCM)
// or the tlsccm module (CCM_8).
type transport string

const (
	viaGCM  transport = "GCM"
	viaCCM8 transport = "CCM_8"
)

// newClient returns a client of s with the certificate of d.
func newClient(t *testing.T, s *serverContainer, p *pki, d device, via transport, codec client.Codec) *client.Client {
	t.Helper()
	hc := client.NewHTTPClient(d.tlsCertificate(), p.roots)
	if via == viaCCM8 {
		hc = tlsccm.NewHTTPClient(d.tlsCertificate(), p.roots, tlsccm.Options{})
	}
	c, err := client.New(client.Config{Server: s.url, HTTPClient: hc, Codec: codec})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// call runs a call operation in the server container s, as an operator
// would, with the payload v.
func (s *serverContainer) call(t *testing.T, op string, v any) result {
	t.Helper()
	payload, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	out, err := docker(context.Background(), "exec", s.id, "ieee20305-interop", "call", op, "--payload", string(payload))
	var r result
	if jerr := json.Unmarshal([]byte(lastLine(out)), &r); jerr != nil {
		t.Fatalf("call %s: no result document (%v): %s", op, err, out)
	}
	return r
}

func lastLine(out string) string {
	line := strings.TrimSpace(out)
	if i := strings.LastIndexByte(line, '\n'); i >= 0 {
		line = line[i+1:]
	}
	return line
}

// result is the result document of a one-shot client operation.
type result struct {
	Adapter   string `json:"adapter"`
	Operation string `json:"operation"`
	OK        bool   `json:"ok"`
	Error     *struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	} `json:"error"`
	Request  json.RawMessage `json:"request"`
	Response json.RawMessage `json:"response"`
	// Output is everything the container wrote.
	Output string `json:"-"`
}

// runOptions are the options of a client run: the baked fixture, and more
// arguments of docker run and of the operation.
type runOptions struct {
	fixture string
	docker  []string
	flags   []string
}

// fixturePath is the path of a baked fixture in an image.
func fixturePath(name string) string { return "/fixtures/" + name + "/fixture.json" }

// runClient runs the one-shot operation op of the client image img against
// url, as the device of the client/ directory of p, and returns its
// result.
func runClient(t *testing.T, img, op, version, url string, p *pki, o runOptions) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	args := append([]string{"run", "--rm", "--add-host", "host.docker.internal:host-gateway",
		"--volume", p.dir + ":/tls:ro"}, o.docker...)
	args = append(args, img, "client", op, "--version", version, "--url", url, "--tls-dir", "/tls/client")
	if o.fixture != "" {
		args = append(args, "--fixture", fixturePath(o.fixture))
	}
	out, err := docker(ctx, append(args, o.flags...)...)
	var r result
	if jerr := json.Unmarshal([]byte(lastLine(out)), &r); jerr != nil {
		t.Fatalf("client %s: no result document (%v): %s", op, err, out)
	}
	r.Output = out
	if err != nil {
		r.Output += "\n" + err.Error()
	}
	return r
}
