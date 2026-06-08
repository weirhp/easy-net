//go:build !windows

package main

func hasTunPrivileges() bool {
	return true
}
