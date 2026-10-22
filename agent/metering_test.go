// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// readingEnd matches the end of a MirrorMeterReading in a document.
var readingEnd = regexp.MustCompile(`</(\w+:)?MirrorMeterReading>`)

func readings(n int) []ieee20305.MirrorMeterReading {
	out := make([]ieee20305.MirrorMeterReading, n)
	for i := range out {
		out[i].MRID = ieee20305.MRIDType{byte(i)}
	}
	return out
}

func TestPostReadingsNone(t *testing.T) {
	f := newFake(t)
	a := newTestAgent(t, f, Config{})
	if err := a.PostReadings(context.Background(), "/mup/1"); err != nil {
		t.Fatal(err)
	}
	if f.count("POST /mup/1") != 0 {
		t.Error("posted no readings")
	}
}

// TestMirror runs the metering of an agent through a failure to create its
// MirrorUsagePoint, a failure to read the meter, failures to post that keep
// the readings (up to maxPendingReadings), a refusal that drops them, and
// posts of one and of several readings.
func TestMirror(t *testing.T) {
	f := newFake(t)
	f.failTimes("POST /mup", 503, 2)
	f.location("POST /mup", "/mup/1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	a := newTestAgent(t, f, Config{
		MirrorUsagePoint: &ieee20305.MirrorUsagePoint{},
		PostRate:         time.Millisecond,
		Readings: func(context.Context) ([]ieee20305.MirrorMeterReading, error) {
			calls++
			switch calls {
			case 1:
				return nil, errors.New("the meter does not answer")
			case 2:
				f.failWith("POST /mup/1", 503)
				return readings(600), nil
			case 3:
				return readings(600), nil
			case 4:
				f.failWith("POST /mup/1", 400)
				return nil, nil
			case 5:
				f.failWith("POST /mup/1", 0)
				return readings(1), nil
			case 6:
				return readings(2), nil
			}
			cancel()
			return nil, nil
		},
	})
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	a.mirror(ctx)
	if f.count("POST /mup") != 3 {
		t.Errorf("MirrorUsagePoint posted %d times, want 3", f.count("POST /mup"))
	}
	st := a.State()
	if st.MirrorUsagePoint != "/mup/1" || st.Posts != 2 {
		t.Errorf("MirrorUsagePoint %q, %d posts", st.MirrorUsagePoint, st.Posts)
	}
	bodies := f.body("POST /mup/1")
	want := []int{600, 1200, 1000, 1, 2}
	if len(bodies) != len(want) {
		t.Fatalf("%d posts of readings, want %d", len(bodies), len(want))
	}
	for i, n := range want {
		if got := len(readingEnd.FindAllString(bodies[i], -1)); got != n {
			t.Errorf("post %d: %d readings, want %d", i, got, n)
		}
	}
}

// TestMirrorStops has the metering stop while it creates its
// MirrorUsagePoint, and once it posts at the postRate of the
// MirrorUsagePoint.
func TestMirrorStops(t *testing.T) {
	f := newFake(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rate := ieee20305.UInt32(3600)
	a := newTestAgent(t, f, Config{
		MirrorUsagePoint: &ieee20305.MirrorUsagePoint{PostRate: &rate},
		Readings: func(context.Context) ([]ieee20305.MirrorMeterReading, error) {
			cancel()
			return readings(1), nil
		},
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	// No Location: the agent tries again until ctx ends.
	creating, stop := context.WithCancel(context.Background())
	defer stop()
	go func() {
		for f.count("POST /mup") < 2 {
			time.Sleep(time.Millisecond)
		}
		stop()
	}()
	a.mirror(creating)
	if a.State().MirrorUsagePoint != "" {
		t.Fatal("a MirrorUsagePoint without a Location")
	}

	f.location("POST /mup", "/mup/1")
	a.mirror(ctx)
	if st := a.State(); st.MirrorUsagePoint != "/mup/1" || st.Posts != 0 {
		t.Errorf("MirrorUsagePoint %q, %d posts", st.MirrorUsagePoint, st.Posts)
	}
}

// fileList is a FileList of the fake server with Files of the hrefs.
func fileList(hrefs ...string) string {
	s := `<FileList ` + ns + ` href="/file" all="` + string(rune('0'+len(hrefs))) + `" results="` + string(rune('0'+len(hrefs))) + `">`
	for _, h := range hrefs {
		s += `<File href="` + h + `"><fileURI>x</fileURI><mfID>1</mfID><mfModel>m</mfModel><mfVer>1</mfVer><size>1</size><type>0000</type></File>`
	}
	return s + `</FileList>`
}

func TestFiles(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	// Two assignments of the same FileList: read once.
	f.set("/edev/1/fsa", `<FunctionSetAssignmentsList `+ns+` href="/edev/1/fsa" all="2" results="2">
		<FunctionSetAssignments href="/fsa/1"><FileListLink href="/file" all="1"/><mRID>F1</mRID></FunctionSetAssignments>
		<FunctionSetAssignments href="/fsa/2"><FileListLink href="/file" all="1"/><mRID>F2</mRID></FunctionSetAssignments>
	</FunctionSetAssignmentsList>`)
	f.set("/file", fileList("/file/1", "/file/2"))
	var got []string
	var fail error
	a := newTestAgent(t, f, Config{OnFile: func(_ context.Context, file *ieee20305.File) error {
		if fail != nil {
			return fail
		}
		got = append(got, file.GetHref())
		return nil
	}})
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if f.count("GET /file") != 1 || len(got) != 2 {
		t.Fatalf("FileList read %d times, files %v", f.count("GET /file"), got)
	}
	// Seen: not passed on again.
	if err := a.poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("files %v", got)
	}
	// A File the server no longer lists is forgotten, and new when it
	// comes back.
	f.set("/file", fileList("/file/1"))
	if err := a.poll(ctx); err != nil {
		t.Fatal(err)
	}
	f.set("/file", fileList("/file/1", "/file/2"))
	if err := a.poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[2] != "/file/2" {
		t.Fatalf("files %v", got)
	}
	// The handler fails: the poll fails, and the File is passed on again.
	f.set("/file", fileList("/file/1", "/file/2", "/file/3"))
	fail = ieee20305.ErrInvalid
	if err := a.poll(ctx); !errors.Is(err, ieee20305.ErrInvalid) {
		t.Fatalf("poll: %v", err)
	}
	fail = nil
	if err := a.poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[3] != "/file/3" {
		t.Fatalf("files %v", got)
	}
}
