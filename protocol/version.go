package protocol

import (
	"fmt"
	"regexp"
	"strconv"
)

var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)$`)

// NewerProtocolError means a plugin needs a protocol minor above Minor: a
// newer Innkeeper runs it.
type NewerProtocolError struct {
	// Needs is the plugin's protocol_version, such as "1.2".
	Needs string
}

func (e *NewerProtocolError) Error() string {
	return fmt.Sprintf("needs protocol %s; this Innkeeper speaks %d.%d", e.Needs, Version, Minor)
}

// CheckVersion reports whether this core speaks a plugin's protocol_version,
// given the protocol major the plugin declares beside it. Empty means
// <major>.0. A minor above Minor is a *NewerProtocolError.
func CheckVersion(major int, version string) error {
	if version == "" {
		return nil
	}
	gotMajor, gotMinor, err := parseVersion(version)
	if err != nil {
		return err
	}
	if gotMajor != major {
		return fmt.Errorf("protocol_version %s doesn't match protocol %d", version, major)
	}
	// Another major's minors aren't ours to compare; the protocol check refuses it.
	if major == Version && gotMinor > Minor {
		return &NewerProtocolError{Needs: version}
	}
	return nil
}

// parseVersion splits a protocol_version, "<major>.<minor>" such as "1.1".
func parseVersion(s string) (major, minor int, err error) {
	m := versionPattern.FindStringSubmatch(s)
	if m != nil {
		major, err = strconv.Atoi(m[1])
		if err == nil {
			minor, err = strconv.Atoi(m[2])
		}
	}
	if m == nil || err != nil {
		return 0, 0, fmt.Errorf("protocol_version %q must be <major>.<minor>, such as \"1.1\"", s)
	}
	return major, minor, nil
}
