package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// The herdr session helpers suspend's resume still reads (herdr exit 10b:
// it moves to the holder in slice C, and this file goes with herdr).

// herdrSessionSocket asks herdr where a session lives (herdr session list
// --json: name, running, socket_path), every 250 ms until it is listed
// running, for 15 s at most. agentbus never computes herdr's config
// directory: herdrSession only parses a path it is given.
func herdrSessionSocket(session string) (string, error) {
	deadline := time.Now().Add(15 * time.Second)
	for {
		out, err := exec.Command(herdrBin(os.Getenv), "session", "list", "--json").Output()
		if err == nil {
			var list struct {
				Sessions []struct {
					Name       string `json:"name"`
					Running    bool   `json:"running"`
					SocketPath string `json:"socket_path"`
				} `json:"sessions"`
			}
			if json.Unmarshal(out, &list) == nil {
				for _, s := range list.Sessions {
					if s.Name == session && s.Running && s.SocketPath != "" {
						return s.SocketPath, nil
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("herdr session %s did not start within 15s", session)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// herdrSessionRunning reads once whether herdr lists the session running.
func herdrSessionRunning(session string) (bool, error) {
	out, err := exec.Command(herdrBin(os.Getenv), "session", "list", "--json").Output()
	if err != nil {
		return false, err
	}
	var list struct {
		Sessions []struct {
			Name    string `json:"name"`
			Running bool   `json:"running"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return false, err
	}
	for _, s := range list.Sessions {
		if s.Name == session {
			return s.Running, nil
		}
	}
	return false, nil
}

// herdrIn runs herdr against one session's socket.
func herdrIn(socket string) func(args ...string) ([]byte, error) {
	return func(args ...string) ([]byte, error) {
		cmd := exec.Command(herdrBin(os.Getenv), args...)
		cmd.Env = append(os.Environ(), "HERDR_SOCKET_PATH="+socket)
		return cmd.Output()
	}
}
