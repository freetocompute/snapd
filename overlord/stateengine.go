// -*- Mode: Go; indent-tabs-mode: t -*-

/*
 * Copyright (C) 2016 Canonical Ltd
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

package overlord

import (
	"github.com/ubuntu-core/snappy/overlord/state"
)

// StateManager is implemented by types responsible for observing
// the system and manipulating it to reflect the desired state.
type StateManager interface {
	// Init hands the manager the current state it's supposed to track
	// and update.  The StateEngine may call Init again after a Stop
	// was observed.
	Init(s *state.State) error

	// Ensure forces a complete evaluation of the current state.
	// See StateEngine.Ensure for more details.
	Ensure() error

	// Stop asks the manager to terminate all activities running concurrently.
	// It must not return before these activities are finished.
	Stop() error
}

// StateEngine controls the dispatching of state changes to state managers.
//
// Most of the actual work performed by the state engine is in fact done
// by the individual managers registered. These managers must be able to
// cope with Ensure calls in any order, coordinating among themselves
// solely via the state.
type StateEngine struct {
	state    *state.State
	managers []StateManager
	inited   bool
}

// NewStateEngine returns a new state engine.
func NewStateEngine(s *state.State) *StateEngine {
	return &StateEngine{
		state: s,
	}
}

// State returns the current system state.
func (se *StateEngine) State() *state.State {
	return se.state
}

// Ensure asks every manager to ensure that they are doing the necessary
// work to put the current desired system state in place by calling their
// respective Ensure methods.
//
// Managers must evaluate the desired state completely when they receive
// that request, and report whether they found any critical issues. They
// must not perform long running activities during that operation, though.
// These should be performed in properly tracked changes and tasks.
func (se *StateEngine) Ensure() error {
	if !se.inited {
		for _, m := range se.managers {
			err := m.Init(se.state)
			if err != nil {
				return err
			}
		}
		se.inited = true
	}

	for _, m := range se.managers {
		err := m.Ensure()
		if err != nil {
			return err
		}
	}
	return nil
}

// AddManager adds the provided manager to take part in state operations.
func (se *StateEngine) AddManager(m StateManager) {
	se.managers = append(se.managers, m)
}

// Stop asks all managers to terminate activities running concurrently.
// It returns the first error found after all managers are stopped.
func (se *StateEngine) Stop() error {
	if se.inited {
		for _, m := range se.managers {
			err := m.Stop()
			if err != nil {
				return err
			}
		}
		se.inited = false
	}
	return nil
}
