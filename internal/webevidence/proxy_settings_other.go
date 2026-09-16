//go:build !windows

package webevidence

func readWebSystemProxy() (webSystemProxy, error) { return webSystemProxy{}, nil }
