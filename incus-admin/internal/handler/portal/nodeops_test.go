package portal

import "testing"

func TestValidateCommand(t *testing.T) {
	cases := []struct {
		name    string
		cmd     string
		ok      bool
		program string
	}{
		{"empty", "", false, ""},
		{"uptime allowed", "uptime", true, "uptime"},
		{"shell meta ; rejected", "uptime; rm -rf /", false, ""},
		{"shell meta && rejected", "uptime && whoami", false, ""},
		{"shell meta pipe rejected", "ps | grep ssh", false, ""},
		{"shell meta subst rejected", "$(whoami)", false, ""},
		{"shell meta backtick rejected", "`id`", false, ""},
		{"shell meta redirect rejected", "cat /etc/hosts > /tmp/x", false, ""},
		{"unknown program rejected", "foobar", false, ""},
		{"systemctl status allowed", "systemctl status incusd", true, "systemctl"},
		{"systemctl restart rejected", "systemctl restart incusd", false, ""},
		{"systemctl without action rejected", "systemctl", false, ""},
		{"cat etc allowed", "cat /etc/hosts", true, "cat"},
		{"cat proc allowed", "cat /proc/meminfo", true, "cat"},
		{"cat non-etc-proc rejected", "cat /root/.ssh/id_rsa", false, ""},
		{"cat without arg rejected", "cat", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			program, _, ok := validateCommand(c.cmd)
			if ok != c.ok {
				t.Errorf("validateCommand(%q) ok=%v, want %v", c.cmd, ok, c.ok)
			}
			if c.ok && program != c.program {
				t.Errorf("validateCommand(%q) program=%q, want %q", c.cmd, program, c.program)
			}
		})
	}
}
