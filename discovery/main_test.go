// SPDX-License-Identifier: MIT

package discovery

import (
	"os"
	"testing"

	"github.com/otfabric/go-ieee20305/internal/leakcheck"
)

// TestMain fails the run when a test leaves a goroutine behind.
func TestMain(m *testing.M) { os.Exit(leakcheck.Main(m)) }
