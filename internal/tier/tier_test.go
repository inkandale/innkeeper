package tier

import "testing"

func TestTiers(t *testing.T) {
	for _, n := range Names {
		got, err := Parse(n)
		if err != nil || got.String() != n {
			t.Errorf("Parse(%q) = %v, %v", n, got, err)
		}
	}
	if _, err := Parse("admin"); err == nil {
		t.Error("parsed an unknown tier")
	}
	if !Destructive.Allows(Read) || !Write.Allows(Write) || Read.Allows(Write) || Write.Allows(Destructive) || Destructive.Allows(0) {
		t.Error("Allows orders tiers wrong")
	}
}
