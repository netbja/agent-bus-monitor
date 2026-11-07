package notify

import (
	"errors"
	"fmt"
	"strings"
)

// Explain is a send's failure in plain words, for the person setting a
// target up (notify-guided-setup §7): what failed and what to check, never
// the URL, the topic or the server's answer. kind is the target's (ntfy,
// discord, desktop): a 401 means another thing on each. "" when err is nil.
func Explain(kind string, err error) string {
	if err == nil {
		return ""
	}
	var se *SendError
	if !errors.As(err, &se) {
		return "the send failed"
	}
	switch {
	case se.Status == 0 && se.Reason == "no connection":
		return "the server cannot be reached: check the address, or your network"
	case se.Status == 0 && se.Reason == "certificate":
		return "the server's certificate is not valid for this address"
	case se.Status == 0 && se.Reason == "timeout":
		return "the server did not answer in 10 s"
	case se.Status == 0 && se.Reason == "no notify-send on PATH":
		return "install libnotify (notify-send) first"
	case se.Status == 0 && strings.HasPrefix(se.Reason, "notify-send failed"):
		return "notify-send failed: is a desktop session running?" + strings.TrimPrefix(se.Reason, "notify-send failed")
	case se.Status == 0:
		return "the send failed (" + se.Reason + ")"
	case kind == "discord" && (se.Status == 401 || se.Status == 404):
		return "this webhook no longer exists, or was not copied whole"
	case kind == "discord" && se.Status == 429:
		return "Discord limits this webhook for now: try again in a minute"
	case se.Status == 401 || se.Status == 403:
		return "the server refused the token (or this topic needs one)"
	case se.Status == 429:
		return "the server limits this machine for now: try again in a minute"
	}
	return fmt.Sprintf("the send failed (%d)", se.Status)
}
