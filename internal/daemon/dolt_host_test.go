package daemon

import (
	"testing"
)

// TestDoltServerHost verifies the host-resolution precedence the daemon uses
// for its client-connects (reaper/backup/compactor): GT_DOLT_HOST env →
// configured DoltServerConfig.Host → "127.0.0.1" default. Without this, those
// daemons pin 127.0.0.1 and write to the laptop instead of following the data
// plane to the hub at cutover (the daemon-hostfix root).
func TestDoltServerHost(t *testing.T) {
	tests := []struct {
		name    string
		envHost string // GT_DOLT_HOST; "" = unset
		cfgHost string // DoltServerConfig.Host; "" = no doltServer/empty
		noMgr   bool   // true = d.doltServer is nil
		want    string
	}{
		{name: "env wins over config", envHost: "10.0.0.5", cfgHost: "192.168.1.9", want: "10.0.0.5"},
		{name: "config host when no env", envHost: "", cfgHost: "192.168.1.9", want: "192.168.1.9"},
		{name: "default when neither", envHost: "", cfgHost: "", want: "127.0.0.1"},
		{name: "default when no doltServer", envHost: "", noMgr: true, want: "127.0.0.1"},
		{name: "env wins even with no doltServer", envHost: "10.0.0.5", noMgr: true, want: "10.0.0.5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// doltServerHost treats an empty GT_DOLT_HOST as unset (h != ""),
			// so t.Setenv("") is equivalent to unset for these cases.
			t.Setenv("GT_DOLT_HOST", tt.envHost)

			d := &Daemon{}
			if !tt.noMgr {
				d.doltServer = &DoltServerManager{config: &DoltServerConfig{Host: tt.cfgHost}}
			}

			if got := d.doltServerHost(); got != tt.want {
				t.Errorf("doltServerHost() = %q, want %q", got, tt.want)
			}
		})
	}
}
