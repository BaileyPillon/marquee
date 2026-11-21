//go:build windows

package library

import (
	"syscall"
	"unsafe"
)

var procGetDiskFreeSpaceEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

func diskSpace(path string) (Space, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return Space{}, err
	}
	var avail, total, totalFree uint64
	r1, _, callErr := procGetDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&avail)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&totalFree)))
	if r1 == 0 {
		return Space{}, callErr
	}
	return Space{Free: avail, Total: total}, nil
}
