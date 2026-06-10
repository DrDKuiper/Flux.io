package sources

import "testing"

func TestValidateConfigPatch(t *testing.T) {
	tests := []struct {
		name    string
		patch   ConfigPatch
		wantErr bool
	}{
		{"valid full", ConfigPatch{DPIMode: ptr("suricata"), ExpectedType: ptr("netflow")}, false},
		{"empty expected ok", ConfigPatch{ExpectedType: ptr("")}, false},
		{"bad dpi", ConfigPatch{DPIMode: ptr("nope")}, true},
		{"bad expected", ConfigPatch{ExpectedType: ptr("sflow")}, true},
		{"nothing set", ConfigPatch{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.patch.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func ptr(s string) *string { return &s }
