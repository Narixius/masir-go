//go:build windows

package main

import "syscall"

const detachedProcess = 0x00000008 // DETACHED_PROCESS: no console tied to this command

func detached() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP}
}
