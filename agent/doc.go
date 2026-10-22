// SPDX-License-Identifier: MIT

// Package agent is the behaviour of an IEEE 2030.5 device on top of package
// client: it finds the server's resources from DeviceCapability, keeps its
// clock in step with the server's, registers its EndDevice and checks the
// PIN of its Registration, and then polls its FunctionSetAssignments at the
// server's pollRate, retrying with backoff when the server fails.
//
// Run does all of this until its context ends. The function sets an agent
// takes part in plug into it as handlers: for DER, DRLC, messaging, pricing
// and prepayment, the agent schedules the events of the device's programs
// with randomization and primacy, passes their transitions to the handler
// of the function set and posts the Responses they ask for.
//
// A device sets the client of its certificate, its identity and the
// handlers of its function sets, and runs the agent:
//
//	a, err := agent.New(agent.Config{
//		Client:    c, // a *client.Client with the device's certificate
//		Identity:  client.IdentityOf(deviceCert),
//		PIN:       111115,
//		VerifyPIN: true,
//		OnDERControl: func(ctx context.Context, t schedule.Transition) error {
//			// Apply or release the control of t.Event as t.To says.
//			return nil
//		},
//	})
//	if err != nil {
//		return err
//	}
//	defer a.Close()
//	return a.Run(ctx)
//
// Start does the registration and returns, leaving the polling to run in
// the background; Active and Events report the events of a function set.
// With Config.NotificationURI and NotificationHandler, the agent subscribes
// to its programs and polls less. Config.Backoff, Logger and Metrics are
// the plug points of the module (see OBSERVABILITY.md).
package agent
