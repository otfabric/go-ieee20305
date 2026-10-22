// SPDX-License-Identifier: MIT

package agent

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/security"
)

// ns is the namespace of the documents of the fake server.
const ns = `xmlns="urn:ieee:std:2030.5:ns"`

// testSFDI is the SFDI of the device of the tests.
const testSFDI = 123456

// fakeServer is an IEEE 2030.5 server of fixed documents, with failures on
// demand, that records the requests it gets.
type fakeServer struct {
	ts *httptest.Server

	mu sync.Mutex
	// docs are the documents GET returns, by path; a path without one is
	// a 404.
	docs map[string]string
	// fail answers "METHOD path" with a status; times[key] times when
	// set, else always.
	fail  map[string]int
	times map[string]int
	// loc is the Location of the answer to "POST path".
	loc map[string]string
	// reqs counts the requests, by "METHOD path"; bodies keeps their
	// bodies.
	reqs   map[string]int
	bodies map[string][]string
}

// newFake returns a fake server with the documents of a registered device
// with one FunctionSetAssignments, which links a DERProgramList and a
// FileList.
func newFake(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{
		docs: map[string]string{}, fail: map[string]int{}, times: map[string]int{}, loc: map[string]string{},
		reqs: map[string]int{}, bodies: map[string][]string{},
	}
	f.ts = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.ts.Close)
	f.set("/dcap", `<DeviceCapability `+ns+` href="/dcap" pollRate="60">
		<TimeLink href="/tm"/>
		<EndDeviceListLink href="/edev" all="1"/>
		<MirrorUsagePointListLink href="/mup" all="0"/>
	</DeviceCapability>`)
	f.set("/tm", fmt.Sprintf(`<Time `+ns+` href="/tm"><currentTime>%d</currentTime><dstEndTime>0</dstEndTime>
		<dstOffset>0</dstOffset><dstStartTime>0</dstStartTime><quality>7</quality><tzOffset>0</tzOffset></Time>`,
		time.Now().Unix()))
	f.setEndDevice(`<RegistrationLink href="/edev/1/rg"/>
		<FunctionSetAssignmentsListLink href="/edev/1/fsa" all="1"/>`)
	f.set("/edev/1/rg", `<Registration `+ns+` href="/edev/1/rg"><dateTimeRegistered>0</dateTimeRegistered><pIN>111115</pIN></Registration>`)
	f.set("/edev/1/fsa", `<FunctionSetAssignmentsList `+ns+` href="/edev/1/fsa" all="1" results="1">
		<FunctionSetAssignments href="/fsa/1">
			<DERProgramListLink href="/derp" all="1"/>
			<FileListLink href="/file" all="1"/>
			<mRID>F1</mRID>
		</FunctionSetAssignments>
	</FunctionSetAssignmentsList>`)
	f.set("/derp", `<DERProgramList `+ns+` href="/derp" all="1" results="1">
		<DERProgram href="/derp/1"><mRID>A1</mRID><DefaultDERControlLink href="/derp/1/dderc"/>
			<DERControlListLink href="/derp/1/derc" all="0"/><primacy>1</primacy></DERProgram>
	</DERProgramList>`)
	f.set("/derp/1/derc", `<DERControlList `+ns+` href="/derp/1/derc" all="0" results="0"/>`)
	f.set("/file", `<FileList `+ns+` href="/file" all="0" results="0"/>`)
	return f
}

// setEndDevice sets the EndDevice of the device, at /edev/1 and in /edev,
// with the links inner.
func (f *fakeServer) setEndDevice(inner string) {
	ed := fmt.Sprintf(`<EndDevice href="/edev/1"><sFDI>%d</sFDI><changedTime>0</changedTime>%s</EndDevice>`, testSFDI, inner)
	f.set("/edev", `<EndDeviceList `+ns+` href="/edev" all="1" results="1">`+ed+`</EndDeviceList>`)
	f.set("/edev/1", strings.Replace(ed, "<EndDevice ", "<EndDevice "+ns+" ", 1))
}

func (f *fakeServer) set(path, doc string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if doc == "" {
		delete(f.docs, path)
		return
	}
	f.docs[path] = doc
}

// failWith answers "METHOD path" with status (none when 0).
func (f *fakeServer) failWith(key string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if status == 0 {
		delete(f.fail, key)
		return
	}
	f.fail[key] = status
}

// failTimes answers "METHOD path" with status the next n times.
func (f *fakeServer) failTimes(key string, status, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[key], f.times[key] = status, n
}

func (f *fakeServer) location(key, loc string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loc[key] = loc
}

// count returns the number of requests "METHOD path".
func (f *fakeServer) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reqs[key]
}

// body returns the bodies of the requests "METHOD path".
func (f *fakeServer) body(key string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bodies[key]...)
}

func (f *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + r.URL.Path
	data, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.reqs[key]++
	f.bodies[key] = append(f.bodies[key], string(data))
	status, doc, loc := f.fail[key], f.docs[r.URL.Path], f.loc[key]
	if n, ok := f.times[key]; ok && status != 0 {
		if n <= 1 {
			delete(f.fail, key)
			delete(f.times, key)
		} else {
			f.times[key] = n - 1
		}
	}
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if doc == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", client.MediaType)
		_, _ = io.WriteString(w, doc)
	case http.MethodPost:
		if loc != "" {
			w.Header().Set("Location", loc)
		}
		w.WriteHeader(http.StatusCreated)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// newTestAgent returns an agent of the device on f with cfg, closed when
// the test ends.
func newTestAgent(t *testing.T, f *fakeServer, cfg Config) *Agent {
	t.Helper()
	c, err := client.New(client.Config{Server: f.ts.URL, HTTPClient: f.ts.Client()})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Client = c
	var lfdi security.LFDI
	for i := range lfdi {
		lfdi[i] = byte(i + 1)
	}
	cfg.Identity = client.Identity{LFDI: lfdi, SFDI: testSFDI}
	if cfg.Backoff.Initial == 0 {
		cfg.Backoff.Initial = time.Millisecond
		cfg.Backoff.Max = 5 * time.Millisecond
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

// waitFor waits for cond, generously.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
