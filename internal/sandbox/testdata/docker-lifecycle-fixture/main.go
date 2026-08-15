package main

import (
	"context"
	"fmt"
	"net"
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
	if len(os.Args) > 1 && os.Args[1] == "network-denied" {
		if networkBypassAvailable() {
			os.Exit(42)
		}
		fmt.Println("docker network boundary remained closed")
	}
}

func networkBypassAvailable() bool {
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		if _, present := os.LookupEnv(name); present {
			return true
		}
	}
	for _, target := range []struct {
		network string
		address string
	}{
		{network: "tcp4", address: "1.1.1.1:53"},
		{network: "tcp6", address: "[2606:4700:4700::1111]:53"},
		{network: "tcp4", address: "172.17.0.1:80"},
	} {
		connection, err := net.DialTimeout(target.network, target.address, 300*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return true
		}
	}
	resolver := net.Resolver{PreferGo: true}
	for _, host := range []string{"example.com", "host.docker.internal"} {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		addresses, err := resolver.LookupHost(ctx, host)
		cancel()
		if err == nil && len(addresses) != 0 {
			return true
		}
	}
	return false
}
