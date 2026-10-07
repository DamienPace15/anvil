package awssite

import (
	"strings"
	"testing"
)

func TestResolveProtection(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		hasWAF      bool
		hasOrigin   bool
		want        string
		wantNotice  string // substring; empty means no notice expected
		wantWarning string // substring; empty means no warning expected
		wantErr     bool
	}{
		// Nothing in front: default none, explicit wins.
		{name: "default", want: ProtectionNone},
		{name: "explicit none", mode: ProtectionNone, want: ProtectionNone},
		{name: "explicit oac", mode: ProtectionOAC, want: ProtectionOAC},
		{name: "explicit edge-oac", mode: ProtectionEdgeOAC, want: ProtectionEdgeOAC},

		// WAF attached: default edge-oac, explicit wins.
		{name: "WAF default", hasWAF: true, want: ProtectionEdgeOAC, wantNotice: `Because a WAF is attached, protection is set to "edge-oac"`},
		{name: "WAF explicit none", mode: ProtectionNone, hasWAF: true, want: ProtectionNone, wantWarning: "while a WAF is attached"},
		{name: "WAF explicit oac", mode: ProtectionOAC, hasWAF: true, want: ProtectionOAC},
		{name: "WAF explicit edge-oac", mode: ProtectionEdgeOAC, hasWAF: true, want: ProtectionEdgeOAC},

		// Origin protection: same rules.
		{name: "origin default", hasOrigin: true, want: ProtectionEdgeOAC, wantNotice: "Because origin protection is enabled"},
		{name: "origin explicit none", mode: ProtectionNone, hasOrigin: true, want: ProtectionNone, wantWarning: "while origin protection is enabled"},
		{name: "origin explicit oac", mode: ProtectionOAC, hasOrigin: true, want: ProtectionOAC},
		{name: "WAF and origin default", hasWAF: true, hasOrigin: true, want: ProtectionEdgeOAC, wantNotice: "a WAF is attached and origin protection is enabled"},

		// Invalid values.
		{name: "invalid", mode: "iam", wantErr: true},
		{name: "removed mode", mode: "oac-with-client-signing", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveProtection(tt.mode, tt.hasWAF, tt.hasOrigin)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.Mode != tt.want {
				t.Errorf("Mode = %q, want %q", got.Mode, tt.want)
			}
			check := func(field, got, want string) {
				if want == "" && got != "" {
					t.Errorf("unexpected %s %q", field, got)
				}
				if want != "" && !strings.Contains(got, want) {
					t.Errorf("%s = %q, want it to contain %q", field, got, want)
				}
			}
			check("Notice", got.Notice, tt.wantNotice)
			check("Warning", got.Warning, tt.wantWarning)
		})
	}
}
