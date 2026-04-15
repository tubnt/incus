package portal

import (
	"testing"
)

func TestParseMetricsForAllVMs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantVMs  int
		wantName string
		checkFn  func(t *testing.T, vms map[string]*VMMetric)
	}{
		{
			name:    "empty input",
			input:   "",
			wantVMs: 0,
		},
		{
			name:    "comments only",
			input:   "# HELP incus_cpu_seconds_total\n# TYPE incus_cpu_seconds_total counter\n",
			wantVMs: 0,
		},
		{
			name: "single VM cpu metrics",
			input: `incus_cpu_seconds_total{cpu="0",mode="user",name="test-vm",project="default",type="virtual-machine"} 100
incus_cpu_seconds_total{cpu="0",mode="system",name="test-vm",project="default",type="virtual-machine"} 50
incus_cpu_seconds_total{cpu="0",mode="idle",name="test-vm",project="default",type="virtual-machine"} 850
`,
			wantVMs:  1,
			wantName: "test-vm",
			checkFn: func(t *testing.T, vms map[string]*VMMetric) {
				m := vms["test-vm"]
				if m.CPUUserPct < 9.9 || m.CPUUserPct > 10.1 {
					t.Errorf("CPUUserPct = %f, want ~10.0", m.CPUUserPct)
				}
				if m.CPUSystemPct < 4.9 || m.CPUSystemPct > 5.1 {
					t.Errorf("CPUSystemPct = %f, want ~5.0", m.CPUSystemPct)
				}
			},
		},
		{
			name: "memory metrics order independent",
			input: `incus_memory_MemFree_bytes{name="vm1",project="default",type="virtual-machine"} 500000000
incus_memory_MemTotal_bytes{name="vm1",project="default",type="virtual-machine"} 1000000000
incus_memory_MemAvailable_bytes{name="vm1",project="default",type="virtual-machine"} 600000000
`,
			wantVMs:  1,
			wantName: "vm1",
			checkFn: func(t *testing.T, vms map[string]*VMMetric) {
				m := vms["vm1"]
				if m.MemTotalBytes != 1000000000 {
					t.Errorf("MemTotalBytes = %d, want 1000000000", m.MemTotalBytes)
				}
				if m.MemUsedBytes != 500000000 {
					t.Errorf("MemUsedBytes = %d, want 500000000", m.MemUsedBytes)
				}
				if m.MemUsedPct < 39.9 || m.MemUsedPct > 40.1 {
					t.Errorf("MemUsedPct = %f, want ~40.0", m.MemUsedPct)
				}
			},
		},
		{
			name: "multiple VMs",
			input: `incus_cpu_seconds_total{cpu="0",mode="user",name="vm-a",project="default",type="virtual-machine"} 10
incus_cpu_seconds_total{cpu="0",mode="user",name="vm-b",project="default",type="virtual-machine"} 20
`,
			wantVMs: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vms := parseMetricsForAllVMs(tt.input)
			if len(vms) != tt.wantVMs {
				t.Errorf("got %d VMs, want %d", len(vms), tt.wantVMs)
			}
			if tt.wantName != "" {
				if _, ok := vms[tt.wantName]; !ok {
					t.Errorf("VM %q not found", tt.wantName)
				}
			}
			if tt.checkFn != nil {
				tt.checkFn(t, vms)
			}
		})
	}
}

func TestExtractLabel(t *testing.T) {
	tests := []struct {
		line string
		key  string
		want string
	}{
		{`incus_cpu_seconds_total{name="test-vm",mode="user"} 100`, "name", "test-vm"},
		{`incus_cpu_seconds_total{name="test-vm",mode="user"} 100`, "mode", "user"},
		{`incus_cpu_seconds_total{name="test-vm"} 100`, "missing", ""},
	}

	for _, tt := range tests {
		got := extractLabel(tt.line, tt.key)
		if got != tt.want {
			t.Errorf("extractLabel(%q, %q) = %q, want %q", tt.line, tt.key, got, tt.want)
		}
	}
}

func TestParseMetricLine(t *testing.T) {
	tests := []struct {
		line     string
		wantName string
		wantVal  float64
	}{
		{`incus_cpu_seconds_total{name="vm"} 123.45`, "incus_cpu_seconds_total", 123.45},
		{`incus_memory_MemTotal_bytes{name="vm"} 1073741824`, "incus_memory_MemTotal_bytes", 1073741824},
		{`no_braces 100`, "", 0},
	}

	for _, tt := range tests {
		name, val := parseMetricLine(tt.line)
		if name != tt.wantName {
			t.Errorf("parseMetricLine(%q) name = %q, want %q", tt.line, name, tt.wantName)
		}
		if val != tt.wantVal {
			t.Errorf("parseMetricLine(%q) val = %f, want %f", tt.line, val, tt.wantVal)
		}
	}
}
