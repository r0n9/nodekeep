//go:build !darwin

package monitor

func fallbackCPUModel() string {
	return ""
}
