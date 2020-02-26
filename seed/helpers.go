// -*- Mode: Go; indent-tabs-mode: t -*-

/*
 * Copyright (C) 2016-2019 Canonical Ltd
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

package seed

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/snapcore/snapd/asserts"
	"github.com/snapcore/snapd/asserts/sysdb"
	"github.com/snapcore/snapd/snap"
)

var trusted = sysdb.Trusted()

func MockTrusted(mockTrusted []asserts.Assertion) (restore func()) {
	prevTrusted := trusted
	trusted = mockTrusted
	return func() {
		trusted = prevTrusted
	}
}

func newMemAssertionsDB() (db asserts.RODatabase, commitTo func(*asserts.Batch) error, err error) {
	memDB, err := asserts.OpenDatabase(&asserts.DatabaseConfig{
		Backstore: asserts.NewMemoryBackstore(),
		Trusted:   trusted,
	})
	if err != nil {
		return nil, nil, err
	}

	commitTo = func(b *asserts.Batch) error {
		return b.CommitTo(memDB, nil)
	}

	return memDB, commitTo, nil
}

func loadAssertions(assertsDir string, loadedFunc func(*asserts.Ref) error) (*asserts.Batch, error) {
	dc, err := ioutil.ReadDir(assertsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoAssertions
		}
		return nil, fmt.Errorf("cannot read assertions dir: %s", err)
	}

	batch := asserts.NewBatch(nil)
	for _, fi := range dc {
		fn := filepath.Join(assertsDir, fi.Name())
		refs, err := readAsserts(batch, fn)
		if err != nil {
			return nil, fmt.Errorf("cannot read assertions: %s", err)
		}
		if loadedFunc != nil {
			for _, ref := range refs {
				if err := loadedFunc(ref); err != nil {
					return nil, err
				}
			}
		}
	}

	return batch, nil
}

func readAsserts(batch *asserts.Batch, fn string) ([]*asserts.Ref, error) {
	f, err := os.Open(fn)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return batch.AddStream(f)
}

func readInfo(snapPath string, si *snap.SideInfo) (*snap.Info, error) {
	snapf, err := snap.Open(snapPath)
	if err != nil {
		return nil, err
	}
	return snap.ReadInfoFromSnapFile(snapf, si)
}

func snapTypeFromModel(modSnap *asserts.ModelSnap) snap.Type {
	switch modSnap.SnapType {
	case "base":
		return snap.TypeBase
	case "core":
		return snap.TypeOS
	case "gadget":
		return snap.TypeGadget
	case "kernel":
		return snap.TypeKernel
	case "snapd":
		return snap.TypeSnapd
	default:
		return snap.TypeApp
	}
}
