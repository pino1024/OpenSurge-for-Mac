package controlapi

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxDeviceAliasLength = 40
	maxDeviceAliases     = 128
)

func mergeDeviceAliases(current, patch map[string]string) (map[string]string, error) {
	next := make(map[string]string, len(current)+len(patch))
	for ip, alias := range current {
		next[ip] = alias
	}
	for rawIP, rawAlias := range patch {
		ip := normalizeTrafficIP(rawIP)
		if ip == "" {
			return nil, fmt.Errorf("device alias IP %q is not a valid IPv4 address", rawIP)
		}
		alias := strings.TrimSpace(rawAlias)
		if alias == "" {
			delete(next, ip)
			continue
		}
		if err := validateDeviceAlias(alias); err != nil {
			return nil, err
		}
		next[ip] = alias
	}
	if len(next) > maxDeviceAliases {
		return nil, fmt.Errorf("at most %d device aliases can be stored", maxDeviceAliases)
	}
	if len(next) == 0 {
		return nil, nil
	}
	return next, nil
}

func validateDeviceAlias(alias string) error {
	if utf8.RuneCountInString(alias) > maxDeviceAliasLength {
		return fmt.Errorf("device aliases can be at most %d characters", maxDeviceAliasLength)
	}
	for _, r := range alias {
		if unicode.IsControl(r) {
			return fmt.Errorf("device aliases cannot contain control characters")
		}
	}
	return nil
}
