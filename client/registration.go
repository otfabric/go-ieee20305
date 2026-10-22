// SPDX-License-Identifier: MIT

package client

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/security"
)

// ErrNotRegistered is matched by a call that needs the registration of an
// EndDevice the server has not registered: it has no RegistrationLink.
var ErrNotRegistered = errors.New("ieee20305: the server has not registered the device")

// ErrPINMismatch is matched by CheckPIN when the PIN of a Registration is
// not the one the device expects: the server registered someone else, or
// the PIN was entered wrongly on the server.
var ErrPINMismatch = errors.New("ieee20305: registration PIN mismatch")

// Identity is the IEEE 2030.5 identity of a device, derived from its
// certificate.
type Identity struct {
	LFDI security.LFDI
	SFDI security.SFDI
}

// IdentityOf returns the identity of the device with certificate cert.
func IdentityOf(cert *x509.Certificate) Identity {
	lfdi := security.CertificateLFDI(cert)
	return Identity{LFDI: lfdi, SFDI: lfdi.SFDI()}
}

// matches reports whether ed is the EndDevice of id: its SFDI is id's, and
// its LFDI, when the server sends one, is id's too.
func (id Identity) matches(ed *ieee20305.EndDevice) bool {
	if uint64(ed.SFDI) != uint64(id.SFDI) {
		return false
	}
	return ed.LFDI == nil || bytes.Equal(*ed.LFDI, id.LFDI[:])
}

// findPageSize is the page size FindEndDevice asks for.
const findPageSize = 100

// FindEndDevice returns the EndDevice of the device id in the EndDeviceList
// at listHref, or nil if the list has none. It asks the server to select the
// device by its SFDI, and pages through the whole list in case the server
// ignores the selection.
func (c *Client) FindEndDevice(ctx context.Context, listHref string, id Identity) (*ieee20305.EndDevice, error) {
	var start uint32
	for {
		var list ieee20305.EndDeviceList
		if err := c.GetList(ctx, listHref, ListOptions{Start: start, Limit: findPageSize, SFDI: uint64(id.SFDI)}, &list); err != nil {
			return nil, err
		}
		for i := range list.EndDevice {
			if id.matches(&list.EndDevice[i]) {
				return &list.EndDevice[i], nil
			}
		}
		n := uint32(len(list.EndDevice))
		start += n
		if n == 0 || start >= uint32(list.All) {
			return nil, nil
		}
	}
}

// Register makes sure the EndDeviceList at listHref has an EndDevice for
// the device id. It returns the one FindEndDevice finds or else posts a new
// one, with the device's SFDI and LFDI, and reads back what the server
// created. created reports whether it posted one.
func (c *Client) Register(ctx context.Context, listHref string, id Identity) (ed *ieee20305.EndDevice, created bool, err error) {
	if ed, err = c.FindEndDevice(ctx, listHref, id); err != nil || ed != nil {
		return ed, false, err
	}
	var post ieee20305.EndDevice
	post.SFDI = ieee20305.SFDIType(id.SFDI)
	lfdi := ieee20305.HexBinary160(id.LFDI[:])
	post.LFDI = &lfdi
	post.ChangedTime = ieee20305.TimeType(time.Now().Unix())
	loc, err := c.Post(ctx, listHref, &post)
	if err != nil {
		return nil, false, err
	}
	if loc == "" {
		ed, err = c.FindEndDevice(ctx, listHref, id)
	} else {
		ed = new(ieee20305.EndDevice)
		err = c.Get(ctx, loc, ed)
	}
	if err != nil {
		return nil, true, err
	}
	if ed == nil || !id.matches(ed) {
		return nil, true, fmt.Errorf("%w: the server did not create an EndDevice for SFDI %d", ieee20305.ErrProtocol, id.SFDI)
	}
	return ed, true, nil
}

// Registration retrieves the Registration of ed. It matches
// ErrNotRegistered when ed has no RegistrationLink.
func (c *Client) Registration(ctx context.Context, ed *ieee20305.EndDevice) (*ieee20305.Registration, error) {
	if ed.RegistrationLink == nil || ed.RegistrationLink.Href == "" {
		return nil, ErrNotRegistered
	}
	var r ieee20305.Registration
	if err := c.Get(ctx, ed.RegistrationLink.Href, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// CheckPIN reports whether r carries the PIN the device was given out of
// band, as a device must check before it trusts the server. A mismatch
// matches ErrPINMismatch.
func CheckPIN(r *ieee20305.Registration, pin uint32) error {
	if uint32(r.PIN) != pin {
		return ErrPINMismatch
	}
	return nil
}
