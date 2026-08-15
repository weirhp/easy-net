//go:build windows

package main

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

const instanceMutexName = "Local\\EasyNetHookEngine"

func acquireInstanceMutex() (bool, func(), error) {
	name, err := windows.UTF16PtrFromString(instanceMutexName)
	if err != nil {
		return false, func() {}, fmt.Errorf("创建引擎互斥锁名称：%w", err)
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if handle == 0 {
		return false, func() {}, fmt.Errorf("创建引擎互斥锁：%w", err)
	}
	release := func() { _ = windows.CloseHandle(handle) }
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		release()
		return false, func() {}, nil
	}
	if err != nil {
		release()
		return false, func() {}, fmt.Errorf("创建引擎互斥锁：%w", err)
	}
	return true, release, nil
}
