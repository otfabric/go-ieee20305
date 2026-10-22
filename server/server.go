// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"crypto/x509"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/schedule"
)

// Profile is what a server needs of the documents it serves: those of
// package ieee20305 by default (StandardProfile), or those of a profile
// package, such as CSIP-AUS, which adds its own resources and elements.
type Profile struct {
	// Codec returns the codec of the documents for a client of edition e.
	Codec func(e ieee20305.Edition) ieee20305.Codec
	// NewElement returns a pointer to a new document of the element name,
	// or nil.
	NewElement func(name xml.Name) any
	// Clone deep-copies a document, or returns nil for a foreign value.
	Clone func(v any) any
	// ElementName returns the document element of a document.
	ElementName func(v any) (xml.Name, bool)
	// Derives reports whether the document element elem is, or derives
	// from, the element of: see ieee20305.Derives.
	Derives func(elem, of string) bool
	// Resources are resources the profile adds to those of the WADL.
	Resources []ieee20305.ResourceSpec
	// Parts are the resources the profile adds to the parts of a resource
	// (see Admin.Create), by WADL resource ID: the ConnectionPoint of an
	// EndDevice in CSIP-AUS, say.
	Parts map[string][]string
}

// StandardProfile serves the documents of package ieee20305.
func StandardProfile() *Profile {
	return &Profile{
		Codec:       func(e ieee20305.Edition) ieee20305.Codec { return e.Codec() },
		NewElement:  ieee20305.NewElement,
		Clone:       ieee20305.Clone,
		ElementName: ieee20305.ElementName,
		Derives:     ieee20305.Derives,
	}
}

// Config configures a Server. The zero value serves an empty resource tree
// with an in-memory store.
type Config struct {
	// Store holds the resources; NewMemStore when nil.
	Store Store
	// Clock is the time of the server; time.Now when nil.
	Clock func() time.Time
	// Profile is the profile of the documents; StandardProfile when nil.
	Profile *Profile
	// DefaultEdition is the edition of IEEE 2030.5 the server speaks to a
	// client whose device record does not say (see DeviceSpec.Edition).
	DefaultEdition ieee20305.Edition
	// PeerCertificates returns the certificate chain of the client of a
	// request: by default r.TLS.PeerCertificates. A TLS stack whose
	// connections are not *crypto/tls.Conn (such as the CCM_8 module)
	// supplies its own.
	PeerCertificates func(r *http.Request) ([]*x509.Certificate, error)
	// MaxRequestBytes limits the size of a request body; 1 MiB when zero.
	MaxRequestBytes int64
	// DefaultLimit is the number of items of a list page when the request
	// gives no l parameter: 1, as IEEE 2030.5 specifies, when zero.
	DefaultLimit uint32
	// MaxLimit caps the l parameter; 255 when zero.
	MaxLimit uint32
	// EnableDiscouraged lists, by WADL resource ID, the methods the WADL
	// discourages that the server supports nonetheless.
	EnableDiscouraged map[string][]string
	// Disable lists, by WADL resource ID, methods the server refuses
	// although the WADL makes them mandatory or optional.
	Disable map[string][]string
	// InBandRegistration lets a device the server was not given
	// (Admin.ProvisionDevice) register itself by posting its EndDevice to the
	// EndDeviceList. The server creates its resources, without a
	// Registration until Admin.Register.
	InBandRegistration bool
	// ClientLists names, by WADL resource ID, lists of every client that
	// clients may post to, besides MirrorUsagePointList and ResponseList.
	ClientLists map[string]bool
	// PollRate is the pollRate of DeviceCapability, in seconds; 900 when
	// zero.
	PollRate uint32
	// NotificationClient sends the notifications of subscriptions, with the
	// certificate of the server as its client certificate. Without one the
	// server takes no subscriptions.
	NotificationClient *http.Client
	// NotificationURIAllowed decides whether the server sends the
	// notifications of a subscription to its notificationURI, which a
	// device chooses: DefaultNotificationURIAllowed when nil. A refused
	// subscription is answered 403.
	NotificationURIAllowed func(u *url.URL) bool
	// NotificationWorkers bounds the notifications sent at once: 16 when
	// zero.
	NotificationWorkers int
	// NotificationAttempts is the number of attempts to deliver a
	// notification before the server cancels its subscription, the first
	// retry after NotificationRetryDelay, doubled after each: 5 and 1s
	// when zero.
	NotificationAttempts   int
	NotificationRetryDelay time.Duration
	// EventClock is the clock of the lifecycle of events (when they become
	// active): Clock, with timers of the operating system, when nil.
	EventClock schedule.Clock
	// OnResponse, when set, is called with each new Response a client
	// posts, and its href.
	OnResponse func(ctx context.Context, href string, response any)
	// MaxListItems caps the lists clients post to, such as LogEvents,
	// Responses and the Readings of mirrors: the oldest items beyond it are
	// deleted. 1000 when zero; no cap when negative. An operator's posts
	// are not capped.
	MaxListItems int
	// EventRetention is how long after its end the server keeps an event in
	// its scheduler (not in its store): 24 hours when zero.
	EventRetention time.Duration
	// FlowPolicy answers the FlowReservationRequests of devices: GrantAll
	// when nil.
	FlowPolicy FlowPolicy
	// Hooks override or extend the handling of resources, by WADL resource
	// ID; they run before the built-in function sets.
	Hooks map[string]Hooks
	// Logger receives the server's log; none when nil.
	Logger *slog.Logger
}

// Server is an IEEE 2030.5 server: an http.Handler to serve over TLS with
// client certificates (security.ServerTLSConfig). It is safe for concurrent
// use.
type Server struct {
	cfg       Config
	profile   *Profile
	store     Store
	clock     func() time.Time
	log       *slog.Logger
	resources map[string]ieee20305.ResourceSpec
	hooks     map[string]Hooks
	notifier  *notifier
	events    *events

	mu       sync.Mutex
	counters map[string]int        // per list or parent: the last id allocated
	devices  map[string]deviceInfo // by LFDI
	closed   bool

	// listLocks serialize the deduplicating creations in a list (Responses,
	// mirrors) and the readings of a device's mirrors.
	listLocks keyLocks

	// regMu serializes the creation of EndDevices, so that a device that
	// posts its EndDevice twice at once gets one.
	regMu sync.Mutex
}

// deviceInfo is what the server knows of a device by its LFDI.
type deviceInfo struct {
	EndDevice string // href
	Edition   ieee20305.Edition
	Role      Role
	ManagedBy string // the LFDI of its aggregator
}

// New returns a Server for cfg.
func New(cfg Config) (*Server, error) {
	s := &Server{cfg: cfg, profile: cfg.Profile, store: cfg.Store, clock: cfg.Clock, log: cfg.Logger,
		resources: map[string]ieee20305.ResourceSpec{}, hooks: map[string]Hooks{},
		counters: map[string]int{}, devices: map[string]deviceInfo{}}
	if s.profile == nil {
		s.profile = StandardProfile()
	}
	if s.profile.Codec == nil || s.profile.NewElement == nil || s.profile.Clone == nil || s.profile.ElementName == nil || s.profile.Derives == nil {
		return nil, fmt.Errorf("%w: a Profile needs Codec, NewElement, Clone, ElementName and Derives", ieee20305.ErrConfiguration)
	}
	if s.store == nil {
		s.store = NewMemStore(s.profile.Clone)
	}
	if s.clock == nil {
		s.clock = time.Now
	}
	if s.log == nil {
		s.log = slog.New(discardHandler{})
	}
	if s.cfg.MaxRequestBytes <= 0 {
		s.cfg.MaxRequestBytes = 1 << 20
	}
	if s.cfg.DefaultLimit == 0 {
		s.cfg.DefaultLimit = 1
	}
	if s.cfg.MaxLimit == 0 {
		s.cfg.MaxLimit = 255
	}
	if s.cfg.PollRate == 0 {
		s.cfg.PollRate = 900
	}
	for _, r := range ieee20305.Resources {
		s.resources[r.ID] = r
	}
	for _, r := range s.profile.Resources {
		if _, dup := s.resources[r.ID]; dup {
			return nil, fmt.Errorf("%w: profile resource %s is already a resource", ieee20305.ErrConfiguration, r.ID)
		}
		s.resources[r.ID] = r
	}
	for id, h := range builtinHooks(s) {
		s.hooks[id] = h
	}
	for id, h := range cfg.Hooks {
		if _, ok := s.resources[id]; !ok {
			return nil, fmt.Errorf("%w: hooks for %s, which is no resource", ieee20305.ErrConfiguration, id)
		}
		s.hooks[id] = h.over(s.hooks[id])
	}
	// What runs in the background starts last, and stops if the server
	// cannot start.
	s.notifier = newNotifier(s)
	s.events = newEvents(s)
	s.store = &watchStore{Store: s.store, s: s}
	if err := s.bootstrap(context.Background()); err != nil {
		s.events.close()
		s.notifier.close()
		return nil, err
	}
	return s, nil
}

// bootstrap creates what every server has: DeviceCapability at /dcap and
// Time at /tm, and the EndDeviceList with in-band registration.
func (s *Server) bootstrap(ctx context.Context) error {
	if _, err := s.store.Get(ctx, "/dcap"); errors.Is(err, ErrNoNode) {
		dc := &ieee20305.DeviceCapability{}
		if err := s.store.Put(ctx, &Node{Href: "/dcap", Resource: "DeviceCapability", Value: dc, Modified: s.clock()}); err != nil {
			return err
		}
	}
	if _, err := s.store.Get(ctx, "/tm"); errors.Is(err, ErrNoNode) {
		if err := s.Admin().Root(ctx, "Time", "/tm", &ieee20305.Time{}); err != nil {
			return err
		}
	}
	if _, err := s.store.Get(ctx, EndDeviceListHref); errors.Is(err, ErrNoNode) && s.cfg.InBandRegistration {
		if err := s.Admin().Root(ctx, "EndDeviceList", EndDeviceListHref, s.newList("EndDeviceList")); err != nil {
			return err
		}
	}
	if err := s.loadDevices(ctx); err != nil {
		return err
	}
	return s.notifier.loadSubscriptions(ctx)
}

// Close stops what the server runs in the background, such as the
// delivery of notifications. The handler answers 503 after Close.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.events.close()
	s.notifier.close()
	return nil
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Store returns the store of the server. Changes made through it notify
// the subscriptions of the resources.
func (s *Server) Store() Store { return s.store }

type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (h discardHandler) WithAttrs([]slog.Attr) slog.Handler      { return h }
func (h discardHandler) WithGroup(string) slog.Handler           { return h }
