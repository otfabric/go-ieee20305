// SPDX-License-Identifier: MIT

package csipaus_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/csipaus"
)

func ptr[T any](v T) *T { return &v }

func documents() map[string]any {
	ctl := &csipaus.DERControl{}
	ctl.MRID = ieee20305.MRIDType{0x01, 0x02, 0x03, 0x04}
	ctl.CreationTime = 1700000000
	ctl.EventStatus.CurrentStatus = 1
	ctl.EventStatus.DateTime = 1700000000
	ctl.Interval = ieee20305.DateTimeInterval{Duration: 300, Start: 1700000060}
	ctl.DERControlBase.OpModExpLimW = &ieee20305.ActivePower{Multiplier: 3, Value: 5}
	ctl.DERControlBase.OpModImpLimW = &ieee20305.ActivePower{Multiplier: 0, Value: 1500}
	ctl.DERControlBase.OpModGenLimW = &ieee20305.ActivePower{Multiplier: 0, Value: 7000}

	edev := &csipaus.EndDevice{}
	edev.Href = ptr("/edev/1")
	edev.SFDI = 16726121139
	edev.ConnectionPointLink = &ieee20305.Link{Href: "/edev/1/cp"}

	cp := &csipaus.ConnectionPointType{ConnectionPointId: "4103859023"}
	cp.Href = ptr("/edev/1/cp")

	set := &csipaus.DERSettings{}
	set.UpdatedTime = 1700000000
	set.SetMaxW = ieee20305.ActivePower{Multiplier: 0, Value: 5000}
	set.SetGradW = 100
	set.DoeModesEnabled = &csipaus.DOEControlType{0x0F}

	capab := &csipaus.DERCapability{DoeModesSupported: csipaus.DOEControlType{0x0F}}
	capab.ModesSupported = ieee20305.DERControlType{0x00, 0x00, 0x00, 0x01}
	capab.RtgMaxW = ieee20305.ActivePower{Multiplier: 0, Value: 5000}
	capab.Type = 4

	return map[string]any{"DERControl": ctl, "EndDevice": edev, "ConnectionPoint": cp, "DERSettings": set, "DERCapability": capab}
}

func TestRoundTrip(t *testing.T) {
	for name, doc := range documents() {
		t.Run(name, func(t *testing.T) {
			if err := doc.(interface{ Validate() error }).Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			out, err := csipaus.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(out, []byte("schemaVer")) {
				t.Errorf("CSIP-AUS documents must not carry schemaVer: %s", out)
			}
			back, err := csipaus.Decode(out)
			if err != nil {
				t.Fatalf("Decode: %v\n%s", err, out)
			}
			if !reflect.DeepEqual(doc, back) {
				t.Errorf("round trip changed the value: %s", out)
			}
		})
	}
}

func TestDecodeUnchangedDocumentUsesBaseType(t *testing.T) {
	v, err := csipaus.Decode([]byte(`<Time xmlns="urn:ieee:std:2030.5:ns"><currentTime>1</currentTime><dstEndTime>0</dstEndTime><dstOffset>0</dstOffset><dstStartTime>0</dstStartTime><quality>7</quality><tzOffset>0</tzOffset></Time>`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.(*ieee20305.Time); !ok {
		t.Errorf("Decode returned %T, want *ieee20305.Time", v)
	}
	if _, err := csipaus.Decode([]byte(`<Nope xmlns="urn:ieee:std:2030.5:ns"/>`)); !errors.Is(err, ieee20305.ErrProtocol) {
		t.Errorf("unknown document: err = %v, want ErrProtocol", err)
	}
}

// TestSchemaValid validates encoded documents against the official CSIP-AUS
// v1.3 schemas composed with the IEEE 2030.5-2018 sep.xsd, with the
// RateComponentLink correction recorded in schemas/manifest.json.
func TestSchemaValid(t *testing.T) {
	up := filepath.Join("..", "schemas", "upstream")
	files := map[string]string{
		"sep.xsd":               filepath.Join(up, "ieee-2030.5-2018", "sep.xsd"),
		"csipaus-core-v1.3.xsd": filepath.Join(up, "csipaus-1.3", "csipaus-core-v1.3.xsd"),
		"csipaus-ext-v1.3.xsd":  filepath.Join(up, "csipaus-1.3", "csipaus-ext-v1.3.xsd"),
	}
	dir := t.TempDir()
	for name, src := range files {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Skip("upstream schemas not downloaded; run make download")
		}
		if name == "csipaus-core-v1.3.xsd" {
			data = bytes.ReplaceAll(data, []byte(`<xs:element ref="RateComponentLink"`), []byte(`<xs:element name="RateComponentLink" type="RateComponentLink"`))
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	xmllint, err := exec.LookPath("xmllint")
	if err != nil {
		t.Skip("xmllint not found")
	}
	for name, doc := range documents() {
		t.Run(name, func(t *testing.T) {
			out, err := csipaus.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(xmllint, "--noout", "--schema", filepath.Join(dir, "csipaus-core-v1.3.xsd"), "-")
			cmd.Stdin = bytes.NewReader(out)
			msg, err := cmd.CombinedOutput()
			if err != nil {
				var lines []string
				for _, l := range strings.Split(string(msg), "\n") {
					if !strings.Contains(l, "Skipping import") {
						lines = append(lines, l)
					}
				}
				t.Errorf("not valid against CSIP-AUS v1.3: %s\n%s", strings.Join(lines, "\n"), out)
			}
		})
	}
}
