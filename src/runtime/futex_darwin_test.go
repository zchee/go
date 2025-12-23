// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin && (amd64 || arm64)

package runtime

import (
	"testing"
	"time"
)

func TestFutexSleepMismatchReturns(t *testing.T) {
	tests := map[string]struct {
		addrValue uint32
		waitValue uint32
		waitNS    int64
		timeout   time.Duration
	}{
		"mismatch: finite timeout": {
			addrValue: 0,
			waitValue: 1,
			waitNS:    int64(5 * time.Second),
			timeout:   200 * time.Millisecond,
		},
		"mismatch: infinite timeout": {
			addrValue: 0,
			waitValue: 1,
			waitNS:    -1,
			timeout:   200 * time.Millisecond,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			addr := tc.addrValue
			done := make(chan struct{})
			go func() {
				futexsleep(&addr, tc.waitValue, tc.waitNS)
				close(done)
			}()

			select {
			case <-done:
				return
			case <-time.After(tc.timeout):
				futexwakeup(&addr, 1)
				t.Fatalf("futexsleep did not return for mismatch within %s (addr=%d wait=%d ns=%d)", tc.timeout, addr, tc.waitValue, tc.waitNS)
			}
		})
	}
}

func TestFutexWakeup(t *testing.T) {
	tests := map[string]struct {
		attempts    int
		waitNS      int64
		waitDelay   time.Duration
		wakeTimeout time.Duration
	}{
		"wake-one: completes": {
			attempts:    5,
			waitNS:      int64(5 * time.Second),
			waitDelay:   5 * time.Millisecond,
			wakeTimeout: 500 * time.Millisecond,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var addr uint32
			woke := false

			for attempt := 0; attempt < tc.attempts; attempt++ {
				addr = 0
				ready := make(chan struct{})
				done := make(chan struct{})
				go func() {
					close(ready)
					futexsleep(&addr, 0, tc.waitNS)
					close(done)
				}()
				<-ready
				time.Sleep(tc.waitDelay)

				select {
				case <-done:
					t.Logf("attempt %d: futexsleep returned before wake (spurious wake)", attempt+1)
					continue
				default:
				}

				futexwakeup(&addr, 1)
				select {
				case <-done:
					woke = true
				case <-time.After(tc.wakeTimeout):
					futexwakeup(&addr, 1)
					t.Fatalf("attempt %d: futexsleep did not wake within %s", attempt+1, tc.wakeTimeout)
				}

				if woke {
					break
				}
			}

			if !woke {
				t.Fatalf("futexsleep returned early in all attempts; wake path not exercised")
			}
		})
	}
}
