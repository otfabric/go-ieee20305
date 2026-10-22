// SPDX-License-Identifier: MIT

// Package csipclient is what a CSIP-AUS device needs beyond package client
// (with csipaus.Codec as its codec) and package agent: the ConnectionPoint
// of its EndDevice. The site limits of the controls it receives are
// csipaus.LimitsOf them.
//
//	ed, err := csipclient.EndDevice(ctx, c, edHref) // c has csipaus.Codec
//	err = csipclient.SetConnectionPoint(ctx, c, ed, "4407123456")
//	limits, ok := csipaus.LimitsOf(control) // from agent.Active(agent.DER)
package csipclient

import (
	"context"
	"fmt"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/csipaus"
)

// EndDevice retrieves the EndDevice at href with the elements of CSIP-AUS,
// such as its ConnectionPointLink.
func EndDevice(ctx context.Context, c *client.Client, href string) (*csipaus.EndDevice, error) {
	var ed csipaus.EndDevice
	if err := c.Get(ctx, href, &ed); err != nil {
		return nil, err
	}
	return &ed, nil
}

// ConnectionPoint retrieves the ConnectionPoint of ed.
func ConnectionPoint(ctx context.Context, c *client.Client, ed *csipaus.EndDevice) (*csipaus.ConnectionPointType, error) {
	if ed.ConnectionPointLink == nil || ed.ConnectionPointLink.Href == "" {
		return nil, fmt.Errorf("%w: the EndDevice has no ConnectionPointLink", ieee20305.ErrProtocol)
	}
	var cp csipaus.ConnectionPointType
	if err := c.Get(ctx, ed.ConnectionPointLink.Href, &cp); err != nil {
		return nil, err
	}
	return &cp, nil
}

// SetConnectionPoint puts the connection point id (the NMI) of the site of
// ed.
func SetConnectionPoint(ctx context.Context, c *client.Client, ed *csipaus.EndDevice, id string) error {
	if ed.ConnectionPointLink == nil || ed.ConnectionPointLink.Href == "" {
		return fmt.Errorf("%w: the EndDevice has no ConnectionPointLink", ieee20305.ErrProtocol)
	}
	return c.Put(ctx, ed.ConnectionPointLink.Href, &csipaus.ConnectionPointType{ConnectionPointId: ieee20305.String32(id)})
}
