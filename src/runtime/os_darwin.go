// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/stringslite"
	"unsafe"
)

type mOS struct {
	waitsema uint32 // semaphore for parking on locks

	// address of errno variable for this thread.
	// This is an optimization to avoid calling libc_error
	// on every syscall_rawsyscalln.
	errnoAddr *int32
}

func unimplemented(name string) {
	println(name, "not implemented")
	*(*int)(unsafe.Pointer(uintptr(1231))) = 1231
}

//go:nosplit
func futexsleep(addr *uint32, val uint32, ns int64) {
	if ns < 0 {
		ulock_wait(1, unsafe.Pointer(addr), val, 0)
		return
	}

	ulock_wait(1, unsafe.Pointer(addr), val, 0)
}

// If any procs are sleeping on addr, wake up at most cnt.
//
//go:nosplit
func futexwakeup(addr *uint32, cnt uint32) {
	ret := ulock_wake(1|0x00000100, unsafe.Pointer(addr), 0)
	if ret >= 0 {
		return
	}

	// I don't know that futex wakeup can return
	// EAGAIN or EINTR, but if it does, it would be
	// safe to loop and call futex again.
	systemstack(func() {
		print("futexwakeup addr=", addr, " returned ", ret, "\n")
	})

	*(*int32)(unsafe.Pointer(uintptr(0x1006))) = 0x1006
}

// The read and write file descriptors used by the sigNote functions.
var sigNoteRead, sigNoteWrite int32

// sigNoteSetup initializes a single, there-can-only-be-one, async-signal-safe note.
//
// The current implementation of notes on Darwin is not async-signal-safe,
// There is only one case where we need to wake up a note from a signal
// handler: the sigsend function. The signal handler code does not require
// all the features of notes: it does not need to do a timed wait.
// This is a separate implementation of notes, based on a pipe, that does
// not support timed waits but is async-signal-safe.
func sigNoteSetup(*note) {
	if sigNoteRead != 0 || sigNoteWrite != 0 {
		// Generalizing this would require avoiding the pipe-fork-closeonexec race, which entangles syscall.
		throw("duplicate sigNoteSetup")
	}
	var errno int32
	sigNoteRead, sigNoteWrite, errno = pipe()
	if errno != 0 {
		throw("pipe failed")
	}
	closeonexec(sigNoteRead)
	closeonexec(sigNoteWrite)

	// Make the write end of the pipe non-blocking, so that if the pipe
	// buffer is somehow full we will not block in the signal handler.
	// Leave the read end of the pipe blocking so that we will block
	// in sigNoteSleep.
	setNonblock(sigNoteWrite)
}

// sigNoteWakeup wakes up a thread sleeping on a note created by sigNoteSetup.
func sigNoteWakeup(*note) {
	var b byte
	write(uintptr(sigNoteWrite), unsafe.Pointer(&b), 1)
}

// sigNoteSleep waits for a note created by sigNoteSetup to be woken.
func sigNoteSleep(*note) {
	for {
		var b byte
		entersyscallblock()
		n := read(sigNoteRead, unsafe.Pointer(&b), 1)
		exitsyscall()
		if n != -_EINTR {
			return
		}
	}
}

// BSD interface for threading.
func osinit() {
	// pthread_create delayed until end of goenvs so that we
	// can look at the environment first.

	numCPUStartup = getCPUCount()
	physPageSize = getPageSize()

	osinit_hack()
}

func sysctlbynameInt32(name []byte) (int32, int32) {
	out := int32(0)
	nout := unsafe.Sizeof(out)
	ret := sysctlbyname(&name[0], (*byte)(unsafe.Pointer(&out)), &nout, nil, 0)
	return ret, out
}

func sysctlbynameBytes(name, out []byte) int32 {
	nout := uintptr(len(out))
	ret := sysctlbyname(&name[0], &out[0], &nout, nil, 0)
	return ret
}

//go:linkname internal_cpu_sysctlbynameInt32 internal/cpu.sysctlbynameInt32
func internal_cpu_sysctlbynameInt32(name []byte) (int32, int32) {
	return sysctlbynameInt32(name)
}

//go:linkname internal_cpu_sysctlbynameBytes internal/cpu.sysctlbynameBytes
func internal_cpu_sysctlbynameBytes(name, out []byte) int32 {
	return sysctlbynameBytes(name, out)
}

const (
	_CTL_HW      = 6
	_HW_NCPU     = 3
	_HW_PAGESIZE = 7
)

func getCPUCount() int32 {
	// Use sysctl to fetch hw.ncpu.
	mib := [2]uint32{_CTL_HW, _HW_NCPU}
	out := uint32(0)
	nout := unsafe.Sizeof(out)
	ret := sysctl(&mib[0], 2, (*byte)(unsafe.Pointer(&out)), &nout, nil, 0)
	if ret >= 0 && int32(out) > 0 {
		return int32(out)
	}
	return 1
}

func getPageSize() uintptr {
	// Use sysctl to fetch hw.pagesize.
	mib := [2]uint32{_CTL_HW, _HW_PAGESIZE}
	out := uint32(0)
	nout := unsafe.Sizeof(out)
	ret := sysctl(&mib[0], 2, (*byte)(unsafe.Pointer(&out)), &nout, nil, 0)
	if ret >= 0 && int32(out) > 0 {
		return uintptr(out)
	}
	return 0
}

//go:nosplit
func readRandom(r []byte) int {
	arc4random_buf(unsafe.Pointer(&r[0]), int32(len(r)))
	return len(r)
}

func goenvs() {
	goenvs_unix()
}

// May run with m.p==nil, so write barriers are not allowed.
//
//go:nowritebarrierrec
func newosproc(mp *m) {
	stk := unsafe.Pointer(mp.g0.stack.hi)
	if false {
		print("newosproc stk=", stk, " m=", mp, " g=", mp.g0, " id=", mp.id, " ostk=", &mp, "\n")
	}

	// Initialize an attribute object.
	var attr pthreadattr
	var err int32
	err = pthread_attr_init(&attr)
	if err != 0 {
		writeErrStr(failthreadcreate)
		exit(1)
	}

	// Find out OS stack size for our own stack guard.
	var stacksize uintptr
	if pthread_attr_getstacksize(&attr, &stacksize) != 0 {
		writeErrStr(failthreadcreate)
		exit(1)
	}
	mp.g0.stack.hi = stacksize // for mstart

	// Tell the pthread library we won't join with this thread.
	if pthread_attr_setdetachstate(&attr, _PTHREAD_CREATE_DETACHED) != 0 {
		writeErrStr(failthreadcreate)
		exit(1)
	}

	// Finally, create the thread. It starts at mstart_stub, which does some low-level
	// setup and then calls mstart.
	var oset sigset
	sigprocmask(_SIG_SETMASK, &sigset_all, &oset)
	err = retryOnEAGAIN(func() int32 {
		return pthread_create(&attr, abi.FuncPCABI0(mstart_stub), unsafe.Pointer(mp))
	})
	sigprocmask(_SIG_SETMASK, &oset, nil)
	if err != 0 {
		writeErrStr(failthreadcreate)
		exit(1)
	}
}

// glue code to call mstart from pthread_create.
func mstart_stub()

// newosproc0 is a version of newosproc that can be called before the runtime
// is initialized.
//
// This function is not safe to use after initialization as it does not pass an M as fnarg.
//
//go:nosplit
func newosproc0(stacksize uintptr, fn uintptr) {
	// Initialize an attribute object.
	var attr pthreadattr
	var err int32
	err = pthread_attr_init(&attr)
	if err != 0 {
		writeErrStr(failthreadcreate)
		exit(1)
	}

	// The caller passes in a suggested stack size,
	// from when we allocated the stack and thread ourselves,
	// without libpthread. Now that we're using libpthread,
	// we use the OS default stack size instead of the suggestion.
	// Find out that stack size for our own stack guard.
	if pthread_attr_getstacksize(&attr, &stacksize) != 0 {
		writeErrStr(failthreadcreate)
		exit(1)
	}
	g0.stack.hi = stacksize // for mstart
	memstats.stacks_sys.add(int64(stacksize))

	// Tell the pthread library we won't join with this thread.
	if pthread_attr_setdetachstate(&attr, _PTHREAD_CREATE_DETACHED) != 0 {
		writeErrStr(failthreadcreate)
		exit(1)
	}

	// Finally, create the thread. It starts at mstart_stub, which does some low-level
	// setup and then calls mstart.
	var oset sigset
	sigprocmask(_SIG_SETMASK, &sigset_all, &oset)
	err = pthread_create(&attr, fn, nil)
	sigprocmask(_SIG_SETMASK, &oset, nil)
	if err != 0 {
		writeErrStr(failthreadcreate)
		exit(1)
	}
}

// Called to do synchronous initialization of Go code built with
// -buildmode=c-archive or -buildmode=c-shared.
// None of the Go runtime is initialized.
//
//go:nosplit
//go:nowritebarrierrec
func libpreinit() {
	initsig(true)
}

// Called to initialize a new m (including the bootstrap m).
// Called on the parent thread (main thread in case of bootstrap), can allocate memory.
func mpreinit(mp *m) {
	mp.gsignal = malg(32 * 1024) // OS X wants >= 8K
	mp.gsignal.m = mp
	if GOOS == "darwin" && GOARCH == "arm64" {
		// mlock the signal stack to work around a kernel bug where it may
		// SIGILL when the signal stack is not faulted in while a signal
		// arrives. See issue 42774.
		mlock(unsafe.Pointer(mp.gsignal.stack.hi-physPageSize), physPageSize)
	}
}

// Called to initialize a new m (including the bootstrap m).
// Called on the new thread, cannot allocate memory.
func minit() {
	// iOS does not support alternate signal stack.
	// The signal handler handles it directly.
	if !(GOOS == "ios" && GOARCH == "arm64") {
		minitSignalStack()
	}
	minitSignalMask()
	getg().m.procid = uint64(pthread_self())
	libc_error_addr(&getg().m.errnoAddr)
}

// Called from dropm to undo the effect of an minit.
//
//go:nosplit
func unminit() {
	// iOS does not support alternate signal stack.
	// See minit.
	if !(GOOS == "ios" && GOARCH == "arm64") {
		unminitSignals()
	}
	getg().m.procid = 0
}

// Called from mexit, but not from dropm, to undo the effect of thread-owned
// resources in minit, semacreate, or elsewhere. Do not take locks after calling this.
//
// This always runs without a P, so //go:nowritebarrierrec is required.
//
//go:nowritebarrierrec
func mdestroy(mp *m) {
}

//go:nosplit
func osyield_no_g() {
	usleep_no_g(1)
}

//go:nosplit
func osyield() {
	usleep(1)
}

const (
	_NSIG        = 32
	_SI_USER     = 0 /* empirically true, but not what headers say */
	_SIG_BLOCK   = 1
	_SIG_UNBLOCK = 2
	_SIG_SETMASK = 3
	_SS_DISABLE  = 4
)

//extern SigTabTT runtime·sigtab[];

type sigset uint32

var sigset_all = ^sigset(0)

//go:nosplit
//go:nowritebarrierrec
func setsig(i uint32, fn uintptr) {
	var sa usigactiont
	sa.sa_flags = _SA_SIGINFO | _SA_ONSTACK | _SA_RESTART
	sa.sa_mask = ^uint32(0)
	if fn == abi.FuncPCABIInternal(sighandler) { // abi.FuncPCABIInternal(sighandler) matches the callers in signal_unix.go
		if iscgo {
			fn = abi.FuncPCABI0(cgoSigtramp)
		} else {
			fn = abi.FuncPCABI0(sigtramp)
		}
	}
	*(*uintptr)(unsafe.Pointer(&sa.__sigaction_u)) = fn
	sigaction(i, &sa, nil)
}

// sigtramp is the callback from libc when a signal is received.
// It is called with the C calling convention.
func sigtramp()
func cgoSigtramp()

//go:nosplit
//go:nowritebarrierrec
func setsigstack(i uint32) {
	var osa usigactiont
	sigaction(i, nil, &osa)
	handler := *(*uintptr)(unsafe.Pointer(&osa.__sigaction_u))
	if osa.sa_flags&_SA_ONSTACK != 0 {
		return
	}
	var sa usigactiont
	*(*uintptr)(unsafe.Pointer(&sa.__sigaction_u)) = handler
	sa.sa_mask = osa.sa_mask
	sa.sa_flags = osa.sa_flags | _SA_ONSTACK
	sigaction(i, &sa, nil)
}

//go:nosplit
//go:nowritebarrierrec
func getsig(i uint32) uintptr {
	var sa usigactiont
	sigaction(i, nil, &sa)
	return *(*uintptr)(unsafe.Pointer(&sa.__sigaction_u))
}

// setSignalstackSP sets the ss_sp field of a stackt.
//
//go:nosplit
func setSignalstackSP(s *stackt, sp uintptr) {
	*(*uintptr)(unsafe.Pointer(&s.ss_sp)) = sp
}

//go:nosplit
//go:nowritebarrierrec
func sigaddset(mask *sigset, i int) {
	*mask |= 1 << (uint32(i) - 1)
}

func sigdelset(mask *sigset, i int) {
	*mask &^= 1 << (uint32(i) - 1)
}

func setProcessCPUProfiler(hz int32) {
	setProcessCPUProfilerTimer(hz)
}

func setThreadCPUProfiler(hz int32) {
	setThreadCPUProfilerHz(hz)
}

//go:nosplit
func validSIGPROF(mp *m, c *sigctxt) bool {
	return true
}

//go:linkname executablePath os.executablePath
var executablePath string

func sysargs(argc int32, argv **byte) {
	// skip over argv, envv and the first string will be the path
	n := argc + 1
	for argv_index(argv, n) != nil {
		n++
	}
	executablePath = gostringnocopy(argv_index(argv, n+1))

	// strip "executable_path=" prefix if available, it's added after OS X 10.11.
	executablePath = stringslite.TrimPrefix(executablePath, "executable_path=")
}

func signalM(mp *m, sig int) {
	pthread_kill(pthread(mp.procid), uint32(sig))
}

// sigPerThreadSyscall is only used on linux, so we assign a bogus signal
// number.
const sigPerThreadSyscall = 1 << 31

//go:nosplit
func runPerThreadSyscall() {
	throw("runPerThreadSyscall only valid on linux")
}
