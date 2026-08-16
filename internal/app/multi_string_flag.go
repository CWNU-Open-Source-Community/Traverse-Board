package app

import "strings"

// multiStringFlag collects repeated flag values.
type multiStringFlag struct {
	values []string
}

func (m *multiStringFlag) String() string {
	if m == nil {
		return ""
	}
	return strings.Join(m.values, ",")
}

func (m *multiStringFlag) Set(value string) error {
	m.values = append(m.values, value)
	return nil
}
