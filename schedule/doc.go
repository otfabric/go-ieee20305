// SPDX-License-Identifier: MIT

// Package schedule implements IEEE 2030.5 event scheduling:
// primacy-based control priority, overlapping event resolution,
// randomization of start and duration, and the
// active/superseded/cancelled state machine.
//
// A Scheduler holds the events of one function set (DER controls, DRLC
// controls, tariff intervals, text messages, ...), which FromEvent makes from
// the generated resources. When events that conflict overlap in time, the
// scheduler resolves them by primacy (lower values win), then by event
// creation time (newer wins): the loser is Superseded, and an active loser
// runs until the winner starts. What conflicts depends on the function set:
// DERConflicts compares the opMod controls of DERControls, DRLCConflicts the
// device categories of EndDeviceControls, and otherwise every event
// conflicts.
//
// A client applies the randomization bounds of the events (Options.Randomize)
// with offsets drawn deterministically from a seed, and answers the
// transitions it subscribes to as ResponseRequired says. A server runs the
// same Scheduler without randomization to keep the EventStatus of its events.
package schedule
