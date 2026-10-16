package backup

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultKeep = 7

// Settings is the backup settings from the environment.
type Settings struct {
	Dir   string
	Every map[Type]time.Duration // only scheduled types
	Keep  map[Type]int           // every type
}

// SettingsFromEnv reads INNKEEPER_BACKUP_DIR and the per-type _EVERY and _KEEP
// variables. Errors name the variable at fault.
func SettingsFromEnv() (Settings, error) {
	c := Settings{
		Dir:   os.Getenv("INNKEEPER_BACKUP_DIR"),
		Every: map[Type]time.Duration{},
		Keep:  map[Type]int{},
	}
	for _, t := range Types {
		name := "INNKEEPER_BACKUP_" + strings.ToUpper(string(t))
		if v := os.Getenv(name + "_EVERY"); v != "" {
			d, err := parseEvery(v)
			if err != nil {
				return Settings{}, fmt.Errorf("%s_EVERY: %w", name, err)
			}
			if c.Dir == "" {
				return Settings{}, fmt.Errorf("%s_EVERY is set but INNKEEPER_BACKUP_DIR is not", name)
			}
			c.Every[t] = d
		}
		c.Keep[t] = defaultKeep
		if v := os.Getenv(name + "_KEEP"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return Settings{}, fmt.Errorf("%s_KEEP: %q is not an integer of at least 1", name, v)
			}
			c.Keep[t] = n
		}
	}
	return c, nil
}

// parseEvery accepts a Go duration or whole days ("7d"), which
// time.ParseDuration lacks.
func parseEvery(s string) (time.Duration, error) {
	var d time.Duration
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, fmt.Errorf("%q is not a duration like 24h or 7d", s)
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, fmt.Errorf("%q is not a duration like 24h or 7d", s)
		}
	}
	if d <= 0 {
		return 0, fmt.Errorf("%q must be positive", s)
	}
	return d, nil
}
