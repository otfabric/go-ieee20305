// SPDX-License-Identifier: MIT

package server

import "time"

// SetNotificationAttemptTimeout sets the timeout of one notification
// attempt of the servers made after it, and returns a func that restores
// it.
func SetNotificationAttemptTimeout(d time.Duration) (restore func()) {
	saved := notificationAttemptTimeout
	notificationAttemptTimeout = d
	return func() { notificationAttemptTimeout = saved }
}
