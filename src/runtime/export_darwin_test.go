// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "internal/runtime/atomic"

var SetNonblock = setNonblock

// FutexsleepUntilDeadlineForTest blocks on addr until at least ns nanoseconds
// have elapsed, retrying across spurious wakeups the way the note layer does. It
// returns the actual elapsed time in nanoseconds so tests can verify that the
// __ulock_wait2 timeout is honored in the correct (nanosecond) unit.
func FutexsleepUntilDeadlineForTest(ns int64) int64 {
	var addr uint32
	start := nanotime()
	deadline := start + ns
	for {
		remaining := deadline - nanotime()
		if remaining <= 0 {
			break
		}
		// addr never changes from 0, so the only way out is the timeout or a
		// spurious wakeup, both of which loop until the deadline passes.
		futexsleep(&addr, atomic.Load(&addr), remaining)
	}
	return nanotime() - start
}

// FutexwakeupNoWaiterForTest wakes addr when no thread is sleeping on it. This
// exercises the -ENOENT path of futexwakeup, which must be treated as success
// rather than crashing.
func FutexwakeupNoWaiterForTest() {
	var addr uint32
	futexwakeup(&addr, 1)
	futexwakeup(&addr, 2) // ULF_WAKE_ALL path
}

// FutexsleepZeroTimeoutForTest calls futexsleep with ns == 0, which must time
// out immediately. A zero timeout is the indefinite-wait encoding for
// __ulock_wait2, so this guards the case from being conflated with ns < 0.
func FutexsleepZeroTimeoutForTest() int64 {
	var addr uint32
	start := nanotime()
	futexsleep(&addr, atomic.Load(&addr), 0)
	return nanotime() - start
}
