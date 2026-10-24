package transport

import "testing"

func TestPipeSDDL(t *testing.T) {
	got, err := pipeSDDL("S-1-5-21-1004336348-1177238915-682003330-1001")
	if err != nil || got != "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;S-1-5-21-1004336348-1177238915-682003330-1001)" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"", "501", "S-1-5-21-1;(A;;GA;;;WD)", "S-1-"} {
		if _, err := pipeSDDL(bad); err == nil {
			t.Errorf("%q should be refused: it could inject access entries", bad)
		}
	}
}
