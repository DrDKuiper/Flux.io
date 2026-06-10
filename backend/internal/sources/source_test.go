package sources

import "testing"

func TestValidDPIMode(t *testing.T) {
	for _, m := range []string{"auto", "suricata", "tzsp", "none"} {
		if !ValidDPIMode(m) {
			t.Errorf("expected %q to be valid", m)
		}
	}
	if ValidDPIMode("bogus") {
		t.Error("expected \"bogus\" to be invalid")
	}
}

func TestValidType(t *testing.T) {
	for _, ty := range []string{"netflow", "tzsp", "suricata"} {
		if !ValidType(ty) {
			t.Errorf("expected %q to be valid", ty)
		}
	}
	if ValidType("") || ValidType("sflow") {
		t.Error("expected empty/sflow to be invalid")
	}
}
