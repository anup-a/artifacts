package guest

import "testing"

func TestClaimCodeRoundTrip(t *testing.T) {
	code := ClaimCode("s", "t_abc")
	id, err := VerifyClaim("s", code)
	if err != nil || id != "t_abc" {
		t.Fatalf("got %q, %v", id, err)
	}
	for _, bad := range []string{"", "t_abc", "t_abc.", "t_xyz." + code[len("t_abc."):], code + "x"} {
		if _, err := VerifyClaim("s", bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := VerifyClaim("other", code); err == nil {
		t.Error("accepted a code signed with another secret")
	}
}
