//go:build darwin

package monitor

import (
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

func fallbackCPUModel() string {
	for _, key := range []string{"machdep.cpu.brand_string", "hw.model"} {
		value, err := unix.Sysctl(key)
		if err != nil {
			continue
		}
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}

	if runtime.GOARCH == "arm64" {
		return "Apple Silicon"
	}
	return ""
}
