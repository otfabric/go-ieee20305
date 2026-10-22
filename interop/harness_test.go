// SPDX-License-Identifier: MIT

//go:build interop

package interop

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/security"
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

// device is a device certificate and its key.
type device struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func (d device) tlsCertificate() tls.Certificate {
	return tls.Certificate{Certificate: [][]byte{d.cert.Raw}, PrivateKey: d.key, Leaf: d.cert}
}

// pki is the TLS material of a run, laid out as the --tls-dir of the
// ieee20305-interop container contract.
type pki struct {
	dir     string // the directory to mount at /tls
	roots   *x509.CertPool
	device  device // provisioned on the server
	other   device // signed by the same CA, not provisioned
	foreign device // signed by another CA
}

// newPKI writes a CA, a server certificate for the names, the device
// certificate the server provisions (server/devices) and two more devices.
func newPKI(t *testing.T, names ...string) *pki {
	t.Helper()
	ca, caKey, err := security.TestCA("ieee20305 interop test CA")
	if err != nil {
		t.Fatal(err)
	}
	serverCert, serverKey, err := security.TestServerCert(ca, caKey, names)
	if err != nil {
		t.Fatal(err)
	}
	newDevice := func(ca *x509.Certificate, key *ecdsa.PrivateKey, name string) device {
		cert, k, err := security.TestDeviceCert(ca, key, name)
		if err != nil {
			t.Fatal(err)
		}
		return device{cert, k}
	}
	otherCA, otherKey, err := security.TestCA("another CA")
	if err != nil {
		t.Fatal(err)
	}
	p := &pki{
		dir:     t.TempDir(),
		roots:   x509.NewCertPool(),
		device:  newDevice(ca, caKey, "interop-device-1"),
		other:   newDevice(ca, caKey, "interop-device-2"),
		foreign: newDevice(otherCA, otherKey, "interop-device-3"),
	}
	p.roots.AddCert(ca)
	server := filepath.Join(p.dir, "server")
	writePEM(t, filepath.Join(server, "server.crt"), "CERTIFICATE", serverCert.Raw)
	writeKey(t, filepath.Join(server, "server.key"), serverKey)
	writePEM(t, filepath.Join(server, "ca.crt"), "CERTIFICATE", ca.Raw)
	writePEM(t, filepath.Join(server, "devices", "interop-device-1.crt"), "CERTIFICATE", p.device.cert.Raw)
	// The container runs as another user: the throwaway material must be
	// readable by it.
	err = filepath.Walk(p.dir, func(path string, info os.FileInfo, err error) error {
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

// server is a running ieee20305-interop server container.
type server struct {
	url string // https://127.0.0.1:<port>
}

// startServer runs image as a server of IEEE 2030.5 version with the TLS
// material of p, and waits until it is ready.
func startServer(t *testing.T, img, version string, p *pki) *server {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := docker(ctx, "run", "--detach", "--publish", "127.0.0.1::443",
		"--volume", p.dir+":/tls:ro", img, "server", "--version", version, "--tls-dir", "/tls/server")
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
	return &server{url: "https://" + addr}
}

// newClient returns a client of s with the certificate of d.
func newClient(t *testing.T, s *server, p *pki, d device, codec client.Codec) *client.Client {
	t.Helper()
	c, err := client.New(client.Config{
		Server:     s.url,
		HTTPClient: client.NewHTTPClient(d.tlsCertificate(), p.roots),
		Codec:      codec,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// fixture is the part of an ieee20305-interop fixture the suite checks.
type fixture struct {
	Device struct {
		PIN uint32 `json:"pin"`
	} `json:"device"`
	DERPrograms []struct {
		Description string `json:"description"`
		Primacy     uint8  `json:"primacy"`
		Controls    []struct {
			Description string         `json:"description"`
			Base        map[string]any `json:"base"`
			Duration    uint32         `json:"duration"`
			Start       uint32         `json:"start"`
		} `json:"controls"`
	} `json:"derPrograms"`
	Report struct {
		MirrorUsagePoint struct {
			MRID                string `json:"mRID"`
			Description         string `json:"description"`
			RoleFlags           string `json:"roleFlags"`
			ServiceCategoryKind uint8  `json:"serviceCategoryKind"`
			Status              uint8  `json:"status"`
		} `json:"mirrorUsagePoint"`
		MeterReadings []struct {
			MRID        string           `json:"mRID"`
			Description string           `json:"description"`
			ReadingType map[string]int64 `json:"readingType"`
			Value       int64            `json:"value"`
		} `json:"meterReadings"`
	} `json:"report"`
}

// bakedFixture reads the fixture name that img seeds its server with.
func bakedFixture(t *testing.T, img, name string) fixture {
	t.Helper()
	out, err := docker(context.Background(), "run", "--rm", img, "print-fixture", name)
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	if err := json.Unmarshal([]byte(out), &f); err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return f
}
