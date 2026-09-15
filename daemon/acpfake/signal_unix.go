package acpfake

import (
	"os"
	"os/signal"
	"syscall"
)

func ignoreSigterm() {
	signal.Ignore(syscall.SIGTERM)
	_ = os.Getpid()
}
