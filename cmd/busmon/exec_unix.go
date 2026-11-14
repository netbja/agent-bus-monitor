package main

import "syscall"

// syscallExec replaces this process with path (the project switch).
func syscallExec(path string, argv, env []string) error { return syscall.Exec(path, argv, env) }
