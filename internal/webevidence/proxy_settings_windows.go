//go:build windows

package webevidence

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func readWebSystemProxy() (webSystemProxy, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return webSystemProxy{}, nil
	}
	if err != nil {
		return webSystemProxy{}, err
	}
	defer key.Close()
	enabled, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return webSystemProxy{}, err
	}
	values := make([]string, 3)
	for index, name := range []string{"ProxyServer", "ProxyOverride", "AutoConfigURL"} {
		values[index], _, err = key.GetStringValue(name)
		if err != nil && !errors.Is(err, registry.ErrNotExist) {
			return webSystemProxy{}, err
		}
	}
	return webSystemProxy{enabled: enabled != 0, server: values[0], bypass: values[1],
		pac: strings.TrimSpace(values[2]) != ""}, nil
}
