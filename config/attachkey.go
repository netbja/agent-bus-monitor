package config

import "regexp"

// The attach prefix's key names (herdr exit spec 4.6): f1-f12, ctrl+<letter>,
// ctrl+\ and ctrl+]. One rule for the setting attach.prefix, the client
// profiles' [keys] tables and agentbus attach --prefix.
var attachKeyRE = regexp.MustCompile(`^(f([1-9]|1[0-2])|ctrl\+([a-z]|\\|\]))$`)

// attachKeyReserved are the ctrl keys a terminal or every client needs:
// interrupt, EOF, backspace, tab, LF, CR, flow control and suspend.
var attachKeyReserved = map[string]bool{"ctrl+c": true, "ctrl+d": true, "ctrl+h": true, "ctrl+i": true,
	"ctrl+j": true, "ctrl+m": true, "ctrl+q": true, "ctrl+s": true, "ctrl+z": true}

// AttachKeyName: k names a key the attach prefix can be (its syntax only).
func AttachKeyName(k string) bool { return attachKeyRE.MatchString(k) }

// AttachPrefixAllowed: k is a key name and not a reserved one.
func AttachPrefixAllowed(k string) bool { return AttachKeyName(k) && !attachKeyReserved[k] }
