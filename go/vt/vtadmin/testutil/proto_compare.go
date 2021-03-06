/*
Copyright 2021 The Vitess Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package testutil

import (
	"testing"

	"github.com/stretchr/testify/assert"

	vtadminpb "vitess.io/vitess/go/vt/proto/vtadmin"
)

// AssertSchemaSlicesEqual is a convenience function to assert that two
// []*vtadminpb.Schema slices are equal, after clearing out any reserved
// proto XXX_ fields.
func AssertSchemaSlicesEqual(t *testing.T, expected []*vtadminpb.Schema, actual []*vtadminpb.Schema, msgAndArgs ...interface{}) {
	t.Helper()

	for _, ss := range [][]*vtadminpb.Schema{expected, actual} {
		for _, s := range ss {
			if s.TableDefinitions != nil {
				for _, td := range s.TableDefinitions {
					td.XXX_sizecache = 0
					td.XXX_unrecognized = nil

					if td.Fields != nil {
						for _, f := range td.Fields {
							f.XXX_sizecache = 0
							f.XXX_unrecognized = nil
						}
					}
				}
			}
		}
	}

	assert.ElementsMatch(t, expected, actual, msgAndArgs...)
}
