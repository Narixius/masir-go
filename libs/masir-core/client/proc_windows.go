//go:build windows

package client

import "syscall"

const detachedProcess = 0x00000008 // DETACHED_PROCESS: no console tied to the parent

func detached() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP}
}
