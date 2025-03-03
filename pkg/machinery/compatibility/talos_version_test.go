// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package compatibility_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/compatibility"
)

type talosVersionTest struct {
	host          string
	target        string
	expectedError string
}

func runTalosVersionTest(t *testing.T, tt talosVersionTest) {
	t.Run(tt.host+" -> "+tt.target, func(t *testing.T) {
		host, err := compatibility.ParseTalosVersion(&machine.VersionInfo{
			Tag: tt.host,
		})
		require.NoError(t, err)

		target, err := compatibility.ParseTalosVersion(&machine.VersionInfo{
			Tag: tt.target,
		})
		require.NoError(t, err)

		err = target.UpgradeableFrom(host)
		if tt.expectedError != "" {
			require.EqualError(t, err, tt.expectedError)
		} else {
			require.NoError(t, err)
		}
	})
}

func TestTalosUpgradeCompatibility13(t *testing.T) {
	for _, tt := range []talosVersionTest{
		{
			host:   "1.2.0",
			target: "1.3.0",
		},
		{
			host:   "1.0.0-alpha.0",
			target: "1.3.0",
		},
		{
			host:   "1.2.0-alpha.0",
			target: "1.3.0-alpha.0",
		},
		{
			host:   "1.3.0",
			target: "1.3.1",
		},
		{
			host:   "1.3.0-beta.0",
			target: "1.3.0",
		},
		{
			host:   "1.4.5",
			target: "1.3.3",
		},
		{
			host:          "0.14.3",
			target:        "1.3.0",
			expectedError: `host version 0.14.3 is too old to upgrade to Talos 1.3.0`,
		},
		{
			host:          "1.5.0-alpha.0",
			target:        "1.3.0",
			expectedError: `host version 1.5.0-alpha.0 is too new to downgrade to Talos 1.3.0`,
		},
	} {
		runTalosVersionTest(t, tt)
	}
}

func TestTalosUpgradeCompatibility14(t *testing.T) {
	for _, tt := range []talosVersionTest{
		{
			host:   "1.3.0",
			target: "1.4.0",
		},
		{
			host:   "1.0.0-alpha.0",
			target: "1.4.0",
		},
		{
			host:   "1.2.0-alpha.0",
			target: "1.4.0-alpha.0",
		},
		{
			host:   "1.4.0",
			target: "1.4.1",
		},
		{
			host:   "1.4.0-beta.0",
			target: "1.4.0",
		},
		{
			host:   "1.5.5",
			target: "1.4.3",
		},
		{
			host:          "0.14.3",
			target:        "1.4.0",
			expectedError: `host version 0.14.3 is too old to upgrade to Talos 1.4.0`,
		},
		{
			host:          "1.6.0-alpha.0",
			target:        "1.4.0",
			expectedError: `host version 1.6.0-alpha.0 is too new to downgrade to Talos 1.4.0`,
		},
	} {
		runTalosVersionTest(t, tt)
	}
}

func TestTalosUpgradeCompatibility15(t *testing.T) {
	for _, tt := range []talosVersionTest{
		{
			host:   "1.3.0",
			target: "1.5.0",
		},
		{
			host:   "1.2.0-alpha.0",
			target: "1.5.0",
		},
		{
			host:   "1.2.0",
			target: "1.5.0-alpha.0",
		},
		{
			host:   "1.5.0",
			target: "1.5.1",
		},
		{
			host:   "1.5.0-beta.0",
			target: "1.5.0",
		},
		{
			host:   "1.6.5",
			target: "1.5.3",
		},
		{
			host:          "1.1.0",
			target:        "1.5.0",
			expectedError: `host version 1.1.0 is too old to upgrade to Talos 1.5.0`,
		},
		{
			host:          "1.7.0-alpha.0",
			target:        "1.5.0",
			expectedError: `host version 1.7.0-alpha.0 is too new to downgrade to Talos 1.5.0`,
		},
	} {
		runTalosVersionTest(t, tt)
	}
}

func TestTalosUpgradeCompatibilityUnsupported(t *testing.T) {
	for _, tt := range []talosVersionTest{
		{
			host:          "1.3.0",
			target:        "1.7.0-alpha.0",
			expectedError: `upgrades to version 1.7.0-alpha.0 are not supported`,
		},
		{
			host:          "1.4.0",
			target:        "1.6.0-alpha.0",
			expectedError: `upgrades to version 1.6.0-alpha.0 are not supported`,
		},
	} {
		runTalosVersionTest(t, tt)
	}
}
