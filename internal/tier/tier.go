// Package tier orders what a caller may do: read, write, or destructive.
// Each grant of access has a tier, and so does each API operation.
package tier

import "fmt"

// Tier is a level of access. A higher tier allows everything below it.
type Tier int

const (
	// Read changes nothing.
	Read Tier = iota + 1
	// Write changes the library, reversibly through revision history.
	Write
	// Destructive can't be undone from Innkeeper, such as a push.
	Destructive
)

var names = map[Tier]string{Read: "read", Write: "write", Destructive: "destructive"}

// Names lists the tiers, lowest first.
var Names = []string{"read", "write", "destructive"}

// Parse returns the tier with this name.
func Parse(s string) (Tier, error) {
	for t, n := range names {
		if n == s {
			return t, nil
		}
	}
	return 0, fmt.Errorf("tier %q must be read, write, or destructive", s)
}

func (t Tier) String() string { return names[t] }

// Allows reports whether a caller with t may do something that needs need.
func (t Tier) Allows(need Tier) bool { return need != 0 && t >= need }
