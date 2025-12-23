// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin && (amd64 || arm64)

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

const (
	darwinULockWait2Unknown uint32 = iota
	darwinULockWait2Supported
	darwinULockWait2Unsupported
)

const (
	_EINVAL = 0x16
	_ENOENT = 0x02
	_ENOSYS = 0x4e
)

const (
	ulock_wait_op ulock_operation = _UL_COMPARE_AND_WAIT | _ULF_NO_ERRNO
	ulock_wake_op ulock_operation = _UL_COMPARE_AND_WAIT | _ULF_NO_ERRNO
)

var darwinULockWait2State uint32

//go:nosplit
func futexsleep(addr *uint32, val uint32, ns int64) {
	if ns == 0 {
		return
	}

	if ns < 0 {
		ret, errno := ulock_wait(ulock_wait_op, unsafe.Pointer(addr), uint64(val), 0)
		if ret >= 0 || errno == _EINTR || errno == _EAGAIN || errno == _ETIMEDOUT {
			return
		}
		systemstack(func() {
			print("__ulock_wait addr=", addr, " val=", val, " ret=", ret, " errno=", errno, "\n")
		})
		*(*int32)(unsafe.Pointer(uintptr(0x1007))) = 0x1007
		return
	}

	deadline := nanotime() + ns
	for {
		wait := deadline - nanotime()
		if wait <= 0 {
			return
		}
		if atomic.Load(&darwinULockWait2State) != darwinULockWait2Unsupported {
			ret, errno := ulock_wait2(ulock_wait_op, unsafe.Pointer(addr), uint64(val), uint64(wait), 0)
			if ret >= 0 || errno == _EINTR || errno == _EAGAIN || errno == _ETIMEDOUT {
				atomic.Cas(&darwinULockWait2State, darwinULockWait2Unknown, darwinULockWait2Supported)
				return
			}
			if errno == _EINVAL || errno == _ENOSYS {
				atomic.Store(&darwinULockWait2State, darwinULockWait2Unsupported)
			} else {
				systemstack(func() {
					print("__ulock_wait2 addr=", addr, " val=", val, " ret=", ret, " errno=", errno, "\n")
				})
				*(*int32)(unsafe.Pointer(uintptr(0x1007))) = 0x1007
				return
			}
		}
		timeout := uint64(wait+999) / 1000
		if timeout > uint64(^uint32(0)) {
			timeout = uint64(^uint32(0))
		}
		ret, errno := ulock_wait(ulock_wait_op, unsafe.Pointer(addr), uint64(val), uint32(timeout))
		if ret >= 0 || errno == _EINTR || errno == _EAGAIN {
			return
		}
		if errno == _ETIMEDOUT {
			continue
		}
		systemstack(func() {
			print("__ulock_wait addr=", addr, " val=", val, " ret=", ret, " errno=", errno, "\n")
		})
		*(*int32)(unsafe.Pointer(uintptr(0x1007))) = 0x1007
		return
	}
}

//go:nosplit
func futexwakeup(addr *uint32, cnt uint32) {
	if cnt == 0 {
		return
	}
	for woke := uint32(0); woke < cnt; {
		ret, errno := ulock_wake(ulock_wake_op, unsafe.Pointer(addr), 0)
		if ret >= 0 {
			woke++
			continue
		}
		switch errno {
		case _EINTR:
			continue
		case _ENOENT, _EAGAIN:
			return
		default:
			systemstack(func() {
				print("__ulock_wake addr=", addr, " cnt=", cnt, " ret=", ret, " errno=", errno, "\n")
			})
			*(*int32)(unsafe.Pointer(uintptr(0x1007))) = 0x1007
			return
		}
	}
}
