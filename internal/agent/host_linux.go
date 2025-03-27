//go:build linux

package agent

import (
	"context"
	"fmt"
	"net"
	"strings"
)

func getAdDomain(context.Context) (*string, error) {
	return nil, nil
}

func getIsAdDomainController(context.Context) (bool, error) {
	return false, nil
}

func getIsEntraConnectServer() (bool, error) {
	return false, nil
}

func getMacAddress() (*string, error) {
	ifas, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, ifa := range ifas {
		a := ifa.HardwareAddr.String()
		if len(a) > 0 {
			// Replace : with empty string
			a = strings.ReplaceAll(a, ":", "")
			return &a, nil
		}
	}

	return nil, fmt.Errorf("%s", "No mac address found")
}

func getEntraDomain(context.Context) (*string, error) {
	return nil, nil
}
