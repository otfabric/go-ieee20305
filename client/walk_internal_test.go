// SPDX-License-Identifier: MIT

package client

import (
	"testing"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// valueList is a Lister that is not a pointer, and has no All.
type valueList struct{}

func (valueList) ListItems() []any             { return nil }
func (valueList) SetListItems([]any) error     { return nil }
func (valueList) NewListItem() any             { return nil }
func (valueList) ListItemElement() string      { return "" }
func (valueList) SetListCounts(uint32, uint32) {}

// noAll is a pointer Lister without an All field.
type noAll struct{ valueList }

func TestListAll(t *testing.T) {
	var nilList *ieee20305.EndDeviceList
	list := &ieee20305.EndDeviceList{}
	list.All = 7
	for _, c := range []struct {
		l    ieee20305.Lister
		want uint32
	}{{list, 7}, {nilList, 0}, {valueList{}, 0}, {&noAll{}, 0}} {
		if got := listAll(c.l); got != c.want {
			t.Errorf("listAll(%T) = %d, want %d", c.l, got, c.want)
		}
	}
}
