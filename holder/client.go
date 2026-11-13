package holder

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// Client is one connection to a holder: the holder checked as same uid
// before anything is sent (security S6, mutual), then hello and welcome.
// The termhost adapter (herdr exit step 6) is built on it.
type Client struct {
	c       *net.UnixConn
	Welcome Welcome
}

// RemoteError is a reply's error.
type RemoteError struct{ Code, Msg string }

func (e *RemoteError) Error() string { return "holder: " + e.Msg + " (" + e.Code + ")" }

// Dial connects to the holder at socket, checks its uid, says hello with
// build, and reads its welcome.
func Dial(socket, build string) (*Client, error) {
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return nil, err
	}
	peer, err := PeerOf(c)
	if err == nil {
		err = CheckSameUser(peer)
	}
	if err != nil {
		c.Close()
		return nil, err
	}
	cl := &Client{c: c}
	hello, _ := EncodeControl(Hello{Proto: Proto, Build: build})
	if err := WriteFrame(c, FrameControl, hello); err != nil {
		c.Close()
		return nil, err
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	typ, body, err := ReadFrame(c)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil || typ != FrameControl {
		c.Close()
		return nil, fmt.Errorf("holder: no welcome: %v", err)
	}
	v, err := DecodeControl(body)
	if w, ok := v.(Welcome); err == nil && ok {
		cl.Welcome = w
		return cl, nil
	}
	var r Reply
	if json.Unmarshal(body, &r) == nil && r.Error != "" {
		c.Close()
		return nil, &RemoteError{Code: r.Code, Msg: r.Error}
	}
	c.Close()
	return nil, errors.New("holder: no welcome")
}

// Call sends one request (a frame struct) and reads its reply; a reply's
// error is a *RemoteError.
func (cl *Client) Call(req any) (Reply, error) {
	raw, err := EncodeControl(req)
	if err != nil {
		return Reply{}, err
	}
	if err := WriteFrame(cl.c, FrameControl, raw); err != nil {
		return Reply{}, err
	}
	typ, body, err := ReadFrame(cl.c)
	if err != nil {
		return Reply{}, err
	}
	if typ != FrameControl {
		return Reply{}, errors.New("holder: a data frame for a reply")
	}
	var r Reply
	if err := json.Unmarshal(body, &r); err != nil || r.Op != "reply" {
		return Reply{}, fmt.Errorf("holder: a malformed reply: %v", err)
	}
	if r.Error != "" {
		return r, &RemoteError{Code: r.Code, Msg: r.Error}
	}
	return r, nil
}

// Close ends the connection.
func (cl *Client) Close() error { return cl.c.Close() }
