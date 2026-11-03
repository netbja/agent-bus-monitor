package config

import (
	"errors"
	"path/filepath"
	"regexp"
)

// ageRecipientRE is an age X25519 recipient: "age1" and 58 bech32 characters.
var ageRecipientRE = regexp.MustCompile(`^age1[qpzry9x8gf2tvdw0s3jn54khce6mua7l]{58}$`)

// ValidAgeRecipient reports whether r is an age public key (age1...), the
// only form backup.age_recipient and --recipient take: not a secret.
func ValidAgeRecipient(r string) bool { return ageRecipientRE.MatchString(r) }

// BackupDir is where archives go (backup spec, "Destination and retention"):
// backup.dir when set, else $XDG_DATA_HOME/agentbus/backups (an absolute one
// only), else ~/.local/share/agentbus/backups. defaulted reports that it
// came from the default: in a service's environment the default can name
// another folder than the person's shell, so with backup.schedule = daily
// agentbus backup warns about it.
func BackupDir(r Resolver, getenv func(string) string) (dir string, defaulted bool, err error) {
	if d := r.String("backup.dir"); d != "" {
		return d, false, nil
	}
	if d := getenv("XDG_DATA_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "agentbus", "backups"), true, nil
	}
	if h := getenv("HOME"); filepath.IsAbs(h) {
		return filepath.Join(h, ".local", "share", "agentbus", "backups"), true, nil
	}
	return "", true, errors.New("no backup folder: set backup.dir in config.toml, or HOME")
}
