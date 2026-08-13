package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "ignore-term" {
		signal.Ignore(syscall.SIGTERM)
		for {
			time.Sleep(time.Hour)
		}
	}
}
