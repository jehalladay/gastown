package daemon

// loadAverage1Sysctl is a no-op on Windows — load average is not a standard metric.
func loadAverage1Sysctl() float64 {
	return 0
}

// availableMemoryGB is a no-op on Windows — pressure checks are not supported.
func availableMemoryGB() float64 {
	return 0
}

// swapUsedPercent is unavailable on Windows (-1 = never trips the shed).
func swapUsedPercent() float64 {
	return -1
}
