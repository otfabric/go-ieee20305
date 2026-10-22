// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/security"
)

// EndDeviceListHref is where the server keeps its EndDeviceList: the
// EndDevices of the devices it registers.
const EndDeviceListHref = "/edev"

// DeviceSpec is a device the operator gives the server: its certificate's
// LFDI and what the server knows of it.
type DeviceSpec struct {
	// LFDI identifies the device by its certificate; see
	// security.CertificateLFDI.
	LFDI security.LFDI
	// Edition is the edition of IEEE 2030.5 the server speaks to it.
	Edition ieee20305.Edition
	// Role is what it may do; an operator has no EndDevice.
	Role Role
	// ManagedBy is the LFDI of the aggregator that manages the device, if
	// any.
	ManagedBy security.LFDI
	// Register registers the device: its EndDevice gets a Registration
	// with PIN, the PIN the device was given out of band.
	Register bool
	PIN      uint32
	// EndDevice is a template of its EndDevice, such as its deviceCategory;
	// the server sets its sFDI, lFDI and links.
	EndDevice *ieee20305.EndDevice
}

// deviceParts are the resources of an EndDevice the server creates with it:
// lists, and resources the device creates with PUT (no value yet).
var deviceParts = []struct {
	resource, path, link string
	list                 bool
}{
	{"FunctionSetAssignmentsList", "fsa", "FunctionSetAssignmentsListLink", true},
	{"DeviceInformation", "di", "DeviceInformationLink", false},
	{"DeviceStatus", "dstat", "DeviceStatusLink", false},
	{"PowerStatus", "ps", "PowerStatusLink", false},
	{"Configuration", "cfg", "ConfigurationLink", false},
	{"FileStatus", "fs", "FileStatusLink", false},
	{"LogEventList", "lel", "LogEventListLink", true},
	{"IPInterfaceList", "ns", "IPInterfaceListLink", true},
	{"LoadShedAvailabilityList", "lsl", "LoadShedAvailabilityListLink", true},
	{"SubscriptionList", "sub", "SubscriptionListLink", true},
	{"DERList", "der", "DERListLink", true},
	{"FlowReservationRequestList", "frq", "FlowReservationRequestListLink", true},
	{"FlowReservationResponseList", "frp", "FlowReservationResponseListLink", true},
}

// ProvisionDevice tells the server of a device, and creates its EndDevice in
// the EndDeviceList (created at EndDeviceListHref if there is none) with
// its resources, unless it is an operator. It returns the href of the
// EndDevice: the existing one for a device the server knows, whose
// registration it updates.
func (a *Admin) ProvisionDevice(ctx context.Context, d DeviceSpec) (string, error) {
	s := a.s
	s.regMu.Lock()
	defer s.regMu.Unlock()
	info := deviceInfo{Edition: d.Edition, Role: d.Role}
	if d.ManagedBy != (security.LFDI{}) {
		info.ManagedBy = d.ManagedBy.String()
	}
	if d.Role == RoleOperator {
		s.setDevice(d.LFDI, info)
		return "", nil
	}
	href := s.device(d.LFDI.String()).EndDevice
	if href == "" {
		var err error
		if href, err = s.newEndDevice(ctx, d.LFDI, d.EndDevice); err != nil {
			return "", err
		}
	}
	info.EndDevice = href
	s.setDevice(d.LFDI, info)
	if d.Register {
		return href, a.Register(ctx, d.LFDI, d.PIN)
	}
	return href, nil
}

// Register registers the device lfdi with pin: its EndDevice gets a
// Registration, or the Registration it has gets the new PIN.
func (a *Admin) Register(ctx context.Context, lfdi security.LFDI, pin uint32) error {
	s := a.s
	ed := s.device(lfdi.String()).EndDevice
	if ed == "" {
		return fmt.Errorf("%w: device %s has no EndDevice", ieee20305.ErrInvalid, lfdi)
	}
	href := path.Join(ed, "rg")
	reg := &ieee20305.Registration{DateTimeRegistered: ieee20305.TimeType(s.clock().Unix()), PIN: ieee20305.PINType(pin)}
	if err := a.Put(ctx, &Node{Href: href, Resource: "Registration", Value: reg, Owner: lfdi.String(), Parent: ed}); err != nil {
		return err
	}
	n, err := s.store.Get(ctx, ed)
	if err != nil {
		return err
	}
	if err := setLink(n.Value, "RegistrationLink", href); err != nil {
		return err
	}
	n.Modified = s.clock()
	return s.store.Put(ctx, n)
}

// RemoveDevice deletes the EndDevice of the device lfdi with its resources,
// and forgets the device.
func (a *Admin) RemoveDevice(ctx context.Context, lfdi security.LFDI) error {
	s := a.s
	s.regMu.Lock()
	defer s.regMu.Unlock()
	d := s.device(lfdi.String())
	s.mu.Lock()
	delete(s.devices, lfdi.String())
	s.mu.Unlock()
	if d.EndDevice == "" {
		return nil
	}
	if err := s.store.Delete(ctx, d.EndDevice); err != nil && !errors.Is(err, ErrNoNode) {
		return err
	}
	return nil
}

// EndDevice returns the href of the EndDevice of the device lfdi, or "".
func (a *Admin) EndDevice(lfdi security.LFDI) string {
	return a.s.device(lfdi.String()).EndDevice
}

// AssignFSA creates a FunctionSetAssignments in the list of the device lfdi
// and returns its href. To give the same FunctionSetAssignments to several
// devices, create it once and AddMember it to their lists.
func (a *Admin) AssignFSA(ctx context.Context, lfdi security.LFDI, fsa *ieee20305.FunctionSetAssignments) (string, error) {
	ed := a.s.device(lfdi.String()).EndDevice
	if ed == "" {
		return "", fmt.Errorf("%w: device %s has no EndDevice", ieee20305.ErrInvalid, lfdi)
	}
	return a.Create(ctx, path.Join(ed, "fsa"), fsa, lfdi.String())
}

func (s *Server) device(lfdi string) deviceInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.devices[lfdi]
}

// knownDevice returns what the server knows of the device lfdi.
func (s *Server) knownDevice(lfdi string) (deviceInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[lfdi]
	return d, ok
}

func (s *Server) setDevice(lfdi security.LFDI, d deviceInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.devices[lfdi.String()] = d
}

// newEndDevice creates the EndDevice of the device lfdi, from the template
// tmpl, with its resources, and returns its href. The caller holds regMu.
func (s *Server) newEndDevice(ctx context.Context, lfdi security.LFDI, tmpl *ieee20305.EndDevice) (string, error) {
	if _, err := s.store.Get(ctx, EndDeviceListHref); errors.Is(err, ErrNoNode) {
		if err := s.Admin().Root(ctx, "EndDeviceList", EndDeviceListHref, &ieee20305.EndDeviceList{}); err != nil {
			return "", err
		}
	}
	ed := &ieee20305.EndDevice{}
	if tmpl != nil {
		ed = tmpl.Clone()
	}
	ed.SFDI = ieee20305.SFDIType(lfdi.SFDI())
	l := ieee20305.HexBinary160(lfdi[:])
	ed.LFDI = &l
	if ed.ChangedTime == 0 {
		ed.ChangedTime = ieee20305.TimeType(s.clock().Unix())
	}
	owner := lfdi.String()
	href := s.newHref(EndDeviceListHref)
	now := s.clock()
	var parts []*Node
	for _, p := range deviceParts {
		h := path.Join(href, p.path)
		n := &Node{Href: h, Resource: p.resource, Owner: owner, Parent: href, Modified: now}
		if p.list {
			n.Value = s.newList(p.resource)
			setHref(n.Value, h)
		}
		if err := setLink(ed, p.link, h); err != nil {
			return "", err
		}
		parts = append(parts, n)
	}
	setHref(ed, href)
	if err := s.store.Put(ctx, &Node{Href: href, Resource: "EndDevice", Value: ed, Owner: owner, Parent: EndDeviceListHref, Modified: now}); err != nil {
		return "", err
	}
	for _, n := range parts {
		if err := s.store.Put(ctx, n); err != nil {
			return "", err
		}
	}
	if err := s.store.AddMember(ctx, EndDeviceListHref, href); err != nil {
		return "", err
	}
	return href, nil
}

// newList returns an empty document of the list resource.
func (s *Server) newList(resource string) any {
	spec := s.resources[resource]
	for _, m := range spec.Methods {
		if m.Method == http.MethodGet {
			return s.profile.NewElement(qname(m.Response))
		}
	}
	return nil
}

// createEndDevice is the POST of an EndDevice to the EndDeviceList:
//
//   - a device that posts its own EndDevice gets the one the server has
//     for it, or a new one with in-band registration (Config);
//   - an aggregator posts the EndDevice of a device it manages, with its
//     lFDI;
//   - an operator posts any EndDevice: with an lFDI, of a device the
//     server then knows.
//
// The sFDI must be that of the lFDI.
func (s *Server) createEndDevice(ctx context.Context, r *Request) (string, error) {
	ed, ok := r.Doc.(*ieee20305.EndDevice)
	if !ok || r.Node.Href != EndDeviceListHref {
		return s.createDefault(ctx, r)
	}
	var lfdi security.LFDI
	hasLFDI := ed.LFDI != nil && len(*ed.LFDI) == len(lfdi)
	if hasLFDI {
		copy(lfdi[:], *ed.LFDI)
		if uint64(lfdi.SFDI()) != uint64(ed.SFDI) {
			return "", &HTTPError{Status: http.StatusBadRequest, Reason: ReasonInvalidRequest,
				Err: fmt.Errorf("sFDI %d is not that of lFDI %s", ed.SFDI, lfdi)}
		}
	}
	p := r.Principal
	own := uint64(ed.SFDI) == uint64(p.SFDI) && (!hasLFDI || lfdi == p.LFDI)
	info := deviceInfo{Edition: p.Edition}
	switch {
	case own && p.Role != RoleOperator:
		lfdi = p.LFDI
		info.Role = p.Role
	case p.Role == RoleAggregator && hasLFDI:
		info.ManagedBy = p.lfdi()
		info.Edition = s.cfg.DefaultEdition
	case p.Role == RoleOperator && hasLFDI:
		info.Edition = s.cfg.DefaultEdition
	case p.Role == RoleOperator:
		return s.createDefault(ctx, r)
	default:
		return "", httpError(http.StatusForbidden, "the EndDevice of sFDI %d is not the client's", ed.SFDI)
	}
	s.regMu.Lock()
	defer s.regMu.Unlock()
	s.mu.Lock()
	known, isKnown := s.devices[lfdi.String()]
	s.mu.Unlock()
	if isKnown && known.EndDevice != "" {
		if info.ManagedBy != "" && known.ManagedBy != info.ManagedBy {
			return "", httpError(http.StatusForbidden, "device %s is not managed by the client", lfdi)
		}
		return known.EndDevice, nil
	}
	if own && !isKnown && !s.cfg.InBandRegistration {
		return "", httpError(http.StatusForbidden, "device %s is not known to the server", lfdi)
	}
	if isKnown {
		info.Edition, info.Role = known.Edition, known.Role
		if known.ManagedBy != "" {
			info.ManagedBy = known.ManagedBy
		}
	}
	href, err := s.newEndDevice(ctx, lfdi, ed)
	if err != nil {
		return "", err
	}
	info.EndDevice = href
	s.setDevice(lfdi, info)
	return href, nil
}

// createDefault creates an item of a list as a POST does without hooks.
func (s *Server) createDefault(ctx context.Context, r *Request) (string, error) {
	if !s.canPost(r.Principal, r.Node) {
		return "", httpError(http.StatusForbidden, "%s is not the client's", r.Node.Href)
	}
	owner := r.Node.Owner
	if owner == "" && r.Principal.Role != RoleOperator {
		owner = r.Principal.lfdi()
	}
	return s.createItem(ctx, r.Node, r.Doc, owner)
}

// deleteEndDevice deletes an EndDevice the client may change, and forgets
// its device.
func (s *Server) deleteEndDevice(ctx context.Context, r *Request) error {
	if !s.canChange(r.Principal, r.Node) {
		return httpError(http.StatusForbidden, "%s is not the client's", r.Node.Href)
	}
	s.regMu.Lock()
	defer s.regMu.Unlock()
	s.mu.Lock()
	for lfdi, d := range s.devices {
		if d.EndDevice == r.Node.Href {
			delete(s.devices, lfdi)
		}
	}
	s.mu.Unlock()
	return s.store.Delete(ctx, r.Node.Href)
}

// operatorOnly refuses a change of a resource the server maintains for the
// device, such as its Registration, to all but an operator.
func (s *Server) operatorOnly(ctx context.Context, r *Request) error {
	if r.Principal.Role != RoleOperator {
		return httpError(http.StatusForbidden, "%s is the server's to change", r.Node.Href)
	}
	if r.Method.Method == http.MethodDelete {
		return s.store.Delete(ctx, r.Node.Href)
	}
	return s.replace(ctx, r)
}

// updateEndDevice is the PUT of an EndDevice: its sFDI and lFDI stay.
func (s *Server) updateEndDevice(ctx context.Context, r *Request) error {
	if ed, ok := r.Doc.(*ieee20305.EndDevice); ok && r.Principal.Role != RoleOperator {
		if old, ok := r.Node.Value.(*ieee20305.EndDevice); ok {
			ed.SFDI, ed.LFDI = old.SFDI, old.LFDI
		}
	}
	return s.replace(ctx, r)
}

// loadDevices learns the devices of the EndDevices of the store, after a
// restart with a persistent store: their editions and roles are the
// defaults until provisioned again.
func (s *Server) loadDevices(ctx context.Context) error {
	hrefs, err := s.store.Hrefs(ctx)
	if err != nil {
		return err
	}
	for _, h := range hrefs {
		n, err := s.store.Get(ctx, h)
		if err != nil || n.Resource != "EndDevice" || n.Owner == "" {
			continue
		}
		lfdi, err := security.ParseLFDI(n.Owner)
		if err != nil {
			continue
		}
		if s.device(n.Owner).EndDevice == "" {
			s.setDevice(lfdi, deviceInfo{EndDevice: h, Edition: s.cfg.DefaultEdition})
		}
	}
	return nil
}
