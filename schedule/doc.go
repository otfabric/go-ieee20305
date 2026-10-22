// SPDX-License-Identifier: MIT

// Package schedule implements IEEE 2030.5 event scheduling:
// primacy-based control priority, overlapping event resolution,
// randomization of start and duration, and the
// active/superseded/cancelled state machine.
//
// When multiple DER programs issue controls that overlap in time, the
// scheduler resolves them by primacy (lower values win), then by event
// creation time, and applies the randomization bounds the server
// specifies. Controls from a higher-primacy program supersede those
// from lower-primacy ones for the overlapping interval.
package schedule
