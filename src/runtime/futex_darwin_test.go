// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin

package runtime_test

import (
	"runtime"
	"testing"
	"time"
)

// TestFutexsleepTimeoutUnit verifies that the darwin futexsleep timeout is
// honored in nanoseconds. The __ulock_wait2 timeout is anchored to the mach
// absolute time clock, whose tick is not one nanosecond on Apple Silicon, so a
// unit mistake would make the wait elapse far too long or far too short. A
// 100ms timed wait must elapse roughly 100ms.
func TestFutexsleepTimeoutUnit(t *testing.T) {
	tests := map[string]struct {
		timeout time.Duration
		lo, hi  time.Duration
	}{
		"100ms wait elapses within [90ms, 400ms]": {
			timeout: 100 * time.Millisecond,
			lo:      90 * time.Millisecond,
			hi:      400 * time.Millisecond,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			elapsedNs := runtime.FutexsleepUntilDeadlineForTest(int64(tc.timeout))
			wall := time.Since(start)
			elapsed := time.Duration(elapsedNs)
			if elapsed < tc.lo || elapsed > tc.hi {
				t.Fatalf("runtime-reported elapsed %v outside [%v, %v]; wall clock %v", elapsed, tc.lo, tc.hi, wall)
			}
			if wall < tc.lo || wall > tc.hi {
				t.Fatalf("wall clock %v outside [%v, %v]; runtime-reported %v", wall, tc.lo, tc.hi, elapsed)
			}
		})
	}
}

// TestFutexwakeupNoWaiter verifies that waking an address with no waiters is a
// no-op rather than a crash. __ulock_wake returns -ENOENT in that case, which
// futexwakeup must tolerate.
func TestFutexwakeupNoWaiter(t *testing.T) {
	// A crash here would take down the test binary; reaching the assertion
	// means the -ENOENT path was handled.
	runtime.FutexwakeupNoWaiterForTest()
}

// TestFutexsleepZeroTimeout verifies that futexsleep with ns == 0 times out
// immediately. __ulock_wait2 treats a zero timeout as an indefinite wait, so a
// naive ns >= 0 encoding would hang here instead of returning; semasleep(0)
// reaches this path on the darwin futex.
func TestFutexsleepZeroTimeout(t *testing.T) {
	elapsed := time.Duration(runtime.FutexsleepZeroTimeoutForTest())
	if elapsed > 10*time.Millisecond {
		t.Fatalf("futexsleep(ns=0) took %v, want immediate return (<10ms)", elapsed)
	}
}
