package main

import "strings"

// herdrBin is the herdr CLI to run: HERDR_BIN_PATH (which herdr injects into
// every plugin command), else HERDR_BIN, else herdr on PATH.
func herdrBin(env func(string) string) string {
	if bin := env("HERDR_BIN_PATH"); bin != "" {
		return bin
	}
	if bin := env("HERDR_BIN"); bin != "" {
		return bin
	}
	return "herdr"
}

// sessionFromSocket reads the session name out of a herdr socket path,
// <config>/sessions/<name>/herdr.sock.
func sessionFromSocket(path string) (string, bool) {
	rest, ok := strings.CutSuffix(path, "/herdr.sock")
	if !ok {
		return "", false
	}
	i := strings.LastIndex(rest, "/")
	if i < 0 || !strings.HasSuffix(rest[:i], "/sessions") || rest[i+1:] == "" {
		return "", false
	}
	return rest[i+1:], true
}
