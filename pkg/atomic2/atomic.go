// Copyright 2020 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package atomic2

import (
	"sync/atomic"
	"unsafe"
)

// AtomicError implements atomic error method
type AtomicError struct {
	p unsafe.Pointer
}

// Get returns error
func (e AtomicError) Get() error {
	return *(*error)(atomic.LoadPointer(&e.p))
}

// Set sets error to AtomicError
func (e AtomicError) Set(err error) {
	atomic.StorePointer(&e.p, unsafe.Pointer(&err))
}
