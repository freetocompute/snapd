// -*- Mode: Go; indent-tabs-mode: t -*-

/*
 * Copyright (C) 2021 Canonical Ltd
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License version 3 as
 * published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package servicestate_test

import (
	"fmt"
	"path/filepath"

	. "gopkg.in/check.v1"

	"github.com/snapcore/snapd/dirs"
	"github.com/snapcore/snapd/gadget/quantity"
	"github.com/snapcore/snapd/overlord/configstate/config"
	"github.com/snapcore/snapd/overlord/servicestate"
	"github.com/snapcore/snapd/overlord/snapstate"
	"github.com/snapcore/snapd/overlord/state"
	"github.com/snapcore/snapd/snap"
	"github.com/snapcore/snapd/snap/snaptest"
	"github.com/snapcore/snapd/snapdenv"
	"github.com/snapcore/snapd/systemd"
	"github.com/snapcore/snapd/testutil"
)

type quotaControlSuite struct {
	baseServiceMgrTestSuite
}

var _ = Suite(&quotaControlSuite{})

func (s *quotaControlSuite) SetUpTest(c *C) {
	s.baseServiceMgrTestSuite.SetUpTest(c)

	// we don't need the EnsureSnapServices ensure loop to run by default
	servicestate.MockEnsuredSnapServices(s.mgr, true)

	// we enable quota-groups by default
	s.state.Lock()
	defer s.state.Unlock()
	tr := config.NewTransaction(s.state)
	tr.Set("core", "experimental.quota-groups", true)
	tr.Commit()

	// mock that we have a new enough version of systemd by default
	r := servicestate.MockSystemdVersion(248)
	s.AddCleanup(r)
}

type quotaGroupState struct {
	MemoryLimit quantity.Size
	SubGroups   []string
	ParentGroup string
	Snaps       []string
}

func checkQuotaState(c *C, st *state.State, exp map[string]quotaGroupState) {
	m, err := servicestate.AllQuotas(st)
	c.Assert(err, IsNil)
	c.Assert(m, HasLen, len(exp))
	for name, grp := range m {
		expGrp, ok := exp[name]
		c.Assert(ok, Equals, true, Commentf("unexpected group %q in state", name))
		c.Assert(grp.MemoryLimit, Equals, expGrp.MemoryLimit)
		c.Assert(grp.ParentGroup, Equals, expGrp.ParentGroup)

		c.Assert(grp.Snaps, HasLen, len(expGrp.Snaps))
		if len(expGrp.Snaps) != 0 {
			c.Assert(grp.Snaps, DeepEquals, expGrp.Snaps)

			// also check on the service file states
			for _, sn := range expGrp.Snaps {
				// meh assume all services are named svc1
				slicePath := name
				if grp.ParentGroup != "" {
					slicePath = grp.ParentGroup + "/" + name
				}
				checkSvcAndSliceState(c, sn+".svc1", slicePath, grp.MemoryLimit)
			}
		}

		c.Assert(grp.SubGroups, HasLen, len(expGrp.SubGroups))
		if len(expGrp.SubGroups) != 0 {
			c.Assert(grp.SubGroups, DeepEquals, expGrp.SubGroups)
		}
	}
}

func checkSvcAndSliceState(c *C, snapSvc string, slicePath string, sliceMem quantity.Size) {
	slicePath = systemd.EscapeUnitNamePath(slicePath)
	// make sure the service file exists
	svcFileName := filepath.Join(dirs.SnapServicesDir, "snap."+snapSvc+".service")
	c.Assert(svcFileName, testutil.FilePresent)

	if sliceMem != 0 {
		// the service file should mention this slice
		c.Assert(svcFileName, testutil.FileContains, fmt.Sprintf("\nSlice=snap.%s.slice\n", slicePath))
	} else {
		c.Assert(svcFileName, Not(testutil.FileContains), fmt.Sprintf("Slice=snap.%s.slice", slicePath))
	}
	checkSliceState(c, slicePath, sliceMem)
}

func checkSliceState(c *C, sliceName string, sliceMem quantity.Size) {
	sliceFileName := filepath.Join(dirs.SnapServicesDir, "snap."+sliceName+".slice")
	if sliceMem != 0 {
		c.Assert(sliceFileName, testutil.FilePresent)
		c.Assert(sliceFileName, testutil.FileContains, fmt.Sprintf("\nMemoryMax=%s\n", sliceMem.String()))
	} else {
		c.Assert(sliceFileName, testutil.FileAbsent)
	}
}

func systemctlCallsForSliceStart(name string) []expectedSystemctl {
	name = systemd.EscapeUnitNamePath(name)
	slice := "snap." + name + ".slice"
	return []expectedSystemctl{
		{expArgs: []string{"start", slice}},
	}
}

func systemctlCallsForSliceStop(name string) []expectedSystemctl {
	name = systemd.EscapeUnitNamePath(name)
	slice := "snap." + name + ".slice"
	return []expectedSystemctl{
		{expArgs: []string{"stop", slice}},
		{
			expArgs: []string{"show", "--property=ActiveState", slice},
			output:  "ActiveState=inactive",
		},
	}
}

func systemctlCallsForServiceRestart(name string) []expectedSystemctl {
	svc := "snap." + name + ".svc1.service"
	return []expectedSystemctl{
		{
			expArgs: []string{"show", "--property=Id,ActiveState,UnitFileState,Type", svc},
			output:  fmt.Sprintf("Id=%s\nActiveState=active\nUnitFileState=enabled\nType=simple\n", svc),
		},
		{expArgs: []string{"stop", svc}},
		{
			expArgs: []string{"show", "--property=ActiveState", svc},
			output:  "ActiveState=inactive",
		},
		{expArgs: []string{"start", svc}},
	}
}

func systemctlCallsForCreateQuota(groupName string, snapNames ...string) []expectedSystemctl {
	calls := join(
		[]expectedSystemctl{{expArgs: []string{"daemon-reload"}}},
		systemctlCallsForSliceStart(groupName),
	)
	for _, snapName := range snapNames {
		calls = join(calls, systemctlCallsForServiceRestart(snapName))
	}

	return calls
}

func systemctlCallsVersion(version int) []expectedSystemctl {
	return []expectedSystemctl{
		{
			expArgs: []string{"--version"},
			output:  fmt.Sprintf("systemd %d\n+FOO +BAR\n", version),
		},
	}
}

func join(calls ...[]expectedSystemctl) []expectedSystemctl {
	fullCall := []expectedSystemctl{}
	for _, call := range calls {
		fullCall = append(fullCall, call...)
	}

	return fullCall
}

func (s *quotaControlSuite) TestCreateQuotaNotEnabled(c *C) {
	s.state.Lock()
	defer s.state.Unlock()
	tr := config.NewTransaction(s.state)
	tr.Set("core", "experimental.quota-groups", false)
	tr.Commit()

	// try to create an empty quota group
	err := servicestate.CreateQuota(s.state, "foo", "", nil, quantity.SizeGiB)
	c.Assert(err, ErrorMatches, `experimental feature disabled - test it by setting 'experimental.quota-groups' to true`)
}

func (s *quotaControlSuite) TestCreateQuotaSystemdTooOld(c *C) {
	s.state.Lock()
	defer s.state.Unlock()

	r := s.mockSystemctlCalls(c, systemctlCallsVersion(204))
	defer r()

	err := servicestate.CheckSystemdVersion()
	c.Assert(err, IsNil)

	err = servicestate.CreateQuota(s.state, "foo", "", nil, quantity.SizeGiB)
	c.Assert(err, ErrorMatches, `systemd version too old: snap quotas requires systemd 205 and newer \(currently have 204\)`)
}

func (s *quotaControlSuite) TestRemoveQuotaPreseeding(c *C) {
	r := snapdenv.MockPreseeding(true)
	defer r()

	st := s.state
	st.Lock()
	defer st.Unlock()

	snapstate.Set(s.state, "test-snap", s.testSnapState)
	snaptest.MockSnapCurrent(c, testYaml, s.testSnapSideInfo)

	// create a quota group
	err := servicestate.CreateQuota(s.state, "foo", "", []string{"test-snap"}, quantity.SizeGiB)
	c.Assert(err, IsNil)

	// check that the quota groups were created in the state
	checkQuotaState(c, st, map[string]quotaGroupState{
		"foo": {
			MemoryLimit: quantity.SizeGiB,
			Snaps:       []string{"test-snap"},
		},
	})

	// but removing a quota doesn't work, since it just doesn't make sense to be
	// able to remove a quota group while preseeding, so we purposely fail
	err = servicestate.RemoveQuota(st, "foo")
	c.Assert(err, ErrorMatches, `removing quota groups not supported while preseeding`)
}

func (s *quotaControlSuite) TestCreateUpdateRemoveQuotaHappy(c *C) {
	r := s.mockSystemctlCalls(c, join(
		// CreateQuota for foo - success
		systemctlCallsForCreateQuota("foo", "test-snap"),

		// UpdateQuota for foo
		[]expectedSystemctl{{expArgs: []string{"daemon-reload"}}},

		// RemoveQuota for foo
		[]expectedSystemctl{{expArgs: []string{"daemon-reload"}}},
		systemctlCallsForSliceStop("foo"),
		[]expectedSystemctl{{expArgs: []string{"daemon-reload"}}},
		systemctlCallsForServiceRestart("test-snap"),
	))
	defer r()

	st := s.state
	st.Lock()
	defer st.Unlock()

	// setup the snap so it exists
	snapstate.Set(s.state, "test-snap", s.testSnapState)
	snaptest.MockSnapCurrent(c, testYaml, s.testSnapSideInfo)

	// create the quota group
	err := servicestate.CreateQuota(st, "foo", "", []string{"test-snap"}, quantity.SizeGiB)
	c.Assert(err, IsNil)

	// check that the quota groups were created in the state
	checkQuotaState(c, st, map[string]quotaGroupState{
		"foo": {
			MemoryLimit: quantity.SizeGiB,
			Snaps:       []string{"test-snap"},
		},
	})

	// increase the memory limit
	err = servicestate.UpdateQuota(st, "foo", servicestate.QuotaGroupUpdate{NewMemoryLimit: 2 * quantity.SizeGiB})
	c.Assert(err, IsNil)

	checkQuotaState(c, st, map[string]quotaGroupState{
		"foo": {
			MemoryLimit: 2 * quantity.SizeGiB,
			Snaps:       []string{"test-snap"},
		},
	})

	// remove the quota
	err = servicestate.RemoveQuota(st, "foo")
	c.Assert(err, IsNil)
	checkQuotaState(c, st, nil)
}

func (s *quotaControlSuite) TestEnsureSnapAbsentFromQuotaGroup(c *C) {
	r := s.mockSystemctlCalls(c, join(
		// CreateQuota for foo
		systemctlCallsForCreateQuota("foo", "test-snap", "test-snap2"),

		// EnsureSnapAbsentFromQuota with just test-snap restarted since it is
		// no longer in the group
		[]expectedSystemctl{{expArgs: []string{"daemon-reload"}}},
		systemctlCallsForServiceRestart("test-snap"),

		// another identical call to EnsureSnapAbsentFromQuota does nothing
		// since the function is idempotent

		// EnsureSnapAbsentFromQuota with just test-snap2 restarted since it is no
		// longer in the group
		[]expectedSystemctl{{expArgs: []string{"daemon-reload"}}},
		systemctlCallsForServiceRestart("test-snap2"),
	))
	defer r()

	st := s.state
	st.Lock()
	defer st.Unlock()
	// setup test-snap
	snapstate.Set(s.state, "test-snap", s.testSnapState)
	snaptest.MockSnapCurrent(c, testYaml, s.testSnapSideInfo)
	// and test-snap2
	si2 := &snap.SideInfo{RealName: "test-snap2", Revision: snap.R(42)}
	snapst2 := &snapstate.SnapState{
		Sequence: []*snap.SideInfo{si2},
		Current:  si2.Revision,
		Active:   true,
		SnapType: "app",
	}
	snapstate.Set(s.state, "test-snap2", snapst2)
	snaptest.MockSnapCurrent(c, testYaml2, si2)

	// create a quota group
	err := servicestate.CreateQuota(s.state, "foo", "", []string{"test-snap", "test-snap2"}, quantity.SizeGiB)
	c.Assert(err, IsNil)

	checkQuotaState(c, st, map[string]quotaGroupState{
		"foo": {
			MemoryLimit: quantity.SizeGiB,
			Snaps:       []string{"test-snap", "test-snap2"},
		},
	})

	// remove test-snap from the group
	err = servicestate.EnsureSnapAbsentFromQuota(s.state, "test-snap")
	c.Assert(err, IsNil)

	checkQuotaState(c, st, map[string]quotaGroupState{
		"foo": {
			MemoryLimit: quantity.SizeGiB,
			Snaps:       []string{"test-snap2"},
		},
	})

	// removing the same snap twice works as well but does nothing
	err = servicestate.EnsureSnapAbsentFromQuota(s.state, "test-snap")
	c.Assert(err, IsNil)

	// now remove test-snap2 too
	err = servicestate.EnsureSnapAbsentFromQuota(s.state, "test-snap2")
	c.Assert(err, IsNil)

	// and check that it got updated in the state
	checkQuotaState(c, st, map[string]quotaGroupState{
		"foo": {
			MemoryLimit: quantity.SizeGiB,
		},
	})

	// it's not an error to call EnsureSnapAbsentFromQuotaGroup on a snap that
	// is not in any quota group
	err = servicestate.EnsureSnapAbsentFromQuota(s.state, "test-snap33333")
	c.Assert(err, IsNil)
}
