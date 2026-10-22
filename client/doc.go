// SPDX-License-Identifier: MIT

// Package client is an IEEE 2030.5 client: it reads and writes the
// resources of one server over HTTPS with mutual TLS, identifying the
// device by its certificate.
//
// A Client resolves the hrefs of resources against its server and refuses
// links and redirects to another server. It reads documents with Get,
// GetList and Fetch, starting from DeviceCapability, and writes them with
// Post, Put and Delete. A failure is a *ieee20305.StatusError carrying the
// server's Error document when it sent one, or matches ieee20305.ErrProtocol
// or ieee20305.ErrTimeout. With csipaus.Codec as its Codec, a client reads
// and writes CSIP-AUS documents.
//
// A device registers with Register: it finds its EndDevice in the server's
// EndDeviceList by the SFDI of its certificate (IdentityOf), or creates it.
// Registration retrieves what the server registered, and CheckPIN checks the
// PIN the device was given out of band.
//
//	c, err := client.New(client.Config{
//		Server:     "https://utility.example:443/dcap",
//		HTTPClient: client.NewHTTPClient(deviceCert, serverCAs),
//	})
//	dcap, err := c.DeviceCapability(ctx)
//	ed, _, err := c.Register(ctx, dcap.EndDeviceListLink.Href, client.IdentityOf(deviceCert.Leaf))
//	reg, err := c.Registration(ctx, ed)
//	err = client.CheckPIN(reg, 111115)
//
// Iterate pages through a list and Walk through the resource tree.
// Subscribe asks for notifications, which NewNotificationHandler receives.
// Config.RetryPolicy, Logger and Metrics are the plug points of the module
// (see OBSERVABILITY.md).
//
// Package agent builds the behaviour of a device on these calls: it
// registers, checks the PIN and polls the device's resources.
package client
