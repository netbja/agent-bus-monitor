package holder

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

// Frames (spec 4.4): a 4-byte big-endian length, a type byte (control JSON
// or raw terminal data), the payload; a frame past MaxFrame is refused
// before its payload is read.
func TestFrames(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, FrameControl, []byte(`{"op":"ping"}`)); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(&buf, FrameData, []byte("\x1b[31mhi")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(&buf, FrameData, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		typ  byte
		body string
	}{{FrameControl, `{"op":"ping"}`}, {FrameData, "\x1b[31mhi"}, {FrameData, ""}} {
		typ, body, err := ReadFrame(&buf)
		if err != nil || typ != want.typ || string(body) != want.body {
			t.Fatalf("frame %d %q %v, want %d %q", typ, body, err, want.typ, want.body)
		}
	}
	if _, _, err := ReadFrame(&buf); err != io.EOF {
		t.Fatalf("end: %v", err)
	}
	// Too large: refused from the header alone.
	var big bytes.Buffer
	binary.Write(&big, binary.BigEndian, uint32(MaxFrame+2))
	big.WriteByte(FrameData)
	if _, _, err := ReadFrame(&big); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("large: %v", err)
	}
	if err := WriteFrame(io.Discard, FrameData, make([]byte, MaxFrame)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("write large: %v", err)
	}
	// An unknown type, a zero length, a cut payload.
	for name, raw := range map[string][]byte{
		"unknown type": {0, 0, 0, 1, 9},
		"zero length":  {0, 0, 0, 0},
		"cut payload":  {0, 0, 0, 5, FrameData, 'a'},
	} {
		if _, _, err := ReadFrame(bytes.NewReader(raw)); err == nil || err == io.EOF {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The control frames are closed sets (spec 4.2, security S2a): one struct
// per op, any field beyond it refused, every value checked.
func TestDecodeControl(t *testing.T) {
	ok := map[string]any{
		`{"op":"hello","proto":1,"build":"v0.29.0-3-gabc"}`: Hello{Proto: 1, Build: "v0.29.0-3-gabc"},
		`{"op":"ping"}`:                 Ping{},
		`{"op":"describe"}`:             Describe{},
		`{"op":"list"}`:                 List{},
		`{"op":"start","role":"coder"}`: Start{Role: "coder"},
		`{"op":"start","role":"coder","profile":"claude-opus","resume_session":"0f1e2d3c-aaaa-bbbb-cccc-000000000001","cols":120,"rows":40}`: Start{Role: "coder", Profile: "claude-opus", ResumeSession: "0f1e2d3c-aaaa-bbbb-cccc-000000000001", Cols: 120, Rows: 40},
		`{"op":"close","term":"t1"}`:                                                       Close{Term: "t1"},
		`{"op":"screen","term":"t1","lines":50}`:                                           ScreenReq{Term: "t1", Lines: 50},
		`{"op":"activity","term":"t1"}`:                                                    ActivityReq{Term: "t1"},
		`{"op":"processes","term":"t1"}`:                                                   Processes{Term: "t1"},
		`{"op":"submit","kind":"wake","term":"t1","nonce":"a1b2c3","count":3}`:             Submit{Kind: "wake", Term: "t1", Nonce: "a1b2c3", Count: 3},
		`{"op":"submit","kind":"wake","term":"t1","nonce":"a1b2c3d4e5f60718","count":999}`: Submit{Kind: "wake", Term: "t1", Nonce: "a1b2c3d4e5f60718", Count: 999},
		`{"op":"submit","kind":"clear","term":"t1"}`:                                       Submit{Kind: "clear", Term: "t1"},
		`{"op":"attach","term":"t1","observe":true}`:                                       Attach{Term: "t1", Observe: true},
		`{"op":"attach","term":"t1","cols":80,"rows":24}`:                                  Attach{Term: "t1", Cols: 80, Rows: 24},
		`{"op":"focus","in":true}`:                                                         Focus{In: true},
		`{"op":"focus","in":false}`:                                                        Focus{},
		`{"op":"resize","cols":90,"rows":30}`:                                              Resize{Cols: 90, Rows: 30},
		`{"op":"detach"}`:                                                                  Detach{},
	}
	for raw, want := range ok {
		got, err := DecodeControl([]byte(raw))
		if err != nil || got != want {
			t.Errorf("%s: %#v %v, want %#v", raw, got, err, want)
		}
	}
	bad := map[string]string{
		// submit (spec 4.2: {kind, term, nonce, count}, no text, no names)
		"a text field":       `{"op":"submit","kind":"wake","term":"t1","nonce":"a1b2c3","count":1,"text":"rm -rf"}`,
		"a name field":       `{"op":"submit","kind":"wake","term":"t1","nonce":"a1b2c3","count":1,"agent":"coder"}`,
		"a project field":    `{"op":"submit","kind":"wake","term":"t1","nonce":"a1b2c3","count":1,"project":"demo"}`,
		"an unknown kind":    `{"op":"submit","kind":"type","term":"t1","nonce":"a1b2c3","count":1}`,
		"no kind":            `{"op":"submit","term":"t1","nonce":"a1b2c3","count":1}`,
		"a short nonce":      `{"op":"submit","kind":"wake","term":"t1","nonce":"a1b2c","count":1}`,
		"a long nonce":       `{"op":"submit","kind":"wake","term":"t1","nonce":"a1b2c3d4e5f607189","count":1}`,
		"an upper nonce":     `{"op":"submit","kind":"wake","term":"t1","nonce":"A1B2C3","count":1}`,
		"a nonce with a key": `{"op":"submit","kind":"wake","term":"t1","nonce":"a1b2c3\r","count":1}`,
		"count 0":            `{"op":"submit","kind":"wake","term":"t1","nonce":"a1b2c3","count":0}`,
		"count 1000":         `{"op":"submit","kind":"wake","term":"t1","nonce":"a1b2c3","count":1000}`,
		"count negative":     `{"op":"submit","kind":"wake","term":"t1","nonce":"a1b2c3","count":-1}`,
		"a clear's nonce":    `{"op":"submit","kind":"clear","term":"t1","nonce":"a1b2c3"}`,
		"a clear's count":    `{"op":"submit","kind":"clear","term":"t1","count":2}`,
		"no term":            `{"op":"submit","kind":"clear"}`,
		"a bad term":         `{"op":"submit","kind":"clear","term":"../x"}`,
		"a term t0":          `{"op":"close","term":"t0"}`,
		"a term with a path": `{"op":"close","term":"t1/../x"}`,
		"a bare t":           `{"op":"screen","term":"t"}`,
		"a term too long":    `{"op":"activity","term":"t1234567890"}`,
		"a build with a CR":  `{"op":"hello","proto":1,"build":"v1\r"}`,
		"a build not ASCII":  `{"op":"hello","proto":1,"build":"v1é"}`,
		// start (security S2a: {role, profile?, resume_session?, size?}, no argv, env, cwd)
		"an argv":       `{"op":"start","role":"coder","argv":["sh","-c","id"]}`,
		"an env":        `{"op":"start","role":"coder","env":["X=1"]}`,
		"a cwd":         `{"op":"start","role":"coder","cwd":"/"}`,
		"a bad role":    `{"op":"start","role":"Coder"}`,
		"no role":       `{"op":"start"}`,
		"a bad profile": `{"op":"start","role":"coder","profile":"sh -c id"}`,
		"a bad session": `{"op":"start","role":"coder","resume_session":"../x"}`,
		"a huge size":   `{"op":"start","role":"coder","cols":100000,"rows":40}`,
		"a half size":   `{"op":"start","role":"coder","cols":80}`,
		// the others
		"an unknown op":      `{"op":"exec","cmd":"id"}`,
		"no op":              `{"term":"t1"}`,
		"a close's extra":    `{"op":"close","term":"t1","signal":"KILL"}`,
		"a ping's extra":     `{"op":"ping","x":1}`,
		"screen lines < 0":   `{"op":"screen","term":"t1","lines":-1}`,
		"screen lines huge":  `{"op":"screen","term":"t1","lines":1000000}`,
		"hello no proto":     `{"op":"hello","build":"v1"}`,
		"hello no build":     `{"op":"hello","proto":1}`,
		"a duplicate op":     `{"op":"ping","op":"start","role":"coder"}`,
		"two objects":        `{"op":"ping"}{"op":"ping"}`,
		"not an object":      `["ping"]`,
		"a case-folded key":  `{"op":"submit","Kind":"wake","term":"t1","nonce":"a1b2c3","count":1}`,
		"a case-folded op":   `{"OP":"ping"}`,
		"a focus's extra":    `{"op":"focus","in":true,"data":"x"}`,
		"a resize of 0":      `{"op":"resize","cols":0,"rows":0}`,
		"a resize too large": `{"op":"resize","cols":80,"rows":5000}`,
		"a detach's extra":   `{"op":"detach","term":"t1"}`,
	}
	for name, raw := range bad {
		if got, err := DecodeControl([]byte(raw)); err == nil {
			t.Errorf("%s: accepted as %#v", name, got)
		}
	}
}

// EncodeControl writes the op beside the struct's fields, and what it writes
// decodes back to the same value.
func TestEncodeControlRoundTrips(t *testing.T) {
	for _, v := range []any{Hello{Proto: Proto, Build: "v1"}, Ping{}, Start{Role: "coder", Cols: 80, Rows: 24},
		Submit{Kind: "clear", Term: "t1"}, Submit{Kind: "wake", Term: "t9", Nonce: "abcdef", Count: 2}, Attach{Term: "t1", Observe: true}} {
		raw, err := EncodeControl(v)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeControl(raw)
		if err != nil || got != v {
			t.Fatalf("%s: %#v %v", raw, got, err)
		}
		if !strings.HasPrefix(string(raw), `{"op":"`) {
			t.Fatalf("op not first: %s", raw)
		}
	}
	if _, err := EncodeControl(struct{ X int }{}); err == nil {
		t.Fatal("an unknown type encoded")
	}
}

// CodeQL on #471 (an allocation size that may overflow): the payload's
// length is checked against the constant bound before any arithmetic. A
// frame (type byte and payload) of MaxFrame-1 and MaxFrame bytes is
// written and read back whole; one of MaxFrame+1 is refused, nothing
// written.
func TestWriteFrameAtTheBound(t *testing.T) {
	for _, c := range []struct {
		frame int
		ok    bool
	}{{MaxFrame - 1, true}, {MaxFrame, true}, {MaxFrame + 1, false}} {
		payload := bytes.Repeat([]byte{'x'}, c.frame-1)
		var buf bytes.Buffer
		err := WriteFrame(&buf, FrameData, payload)
		if !c.ok {
			if !errors.Is(err, ErrFrameTooLarge) || buf.Len() != 0 {
				t.Fatalf("frame %d: err %v, %d bytes written", c.frame, err, buf.Len())
			}
			continue
		}
		if err != nil || buf.Len() != 4+c.frame {
			t.Fatalf("frame %d: err %v, %d bytes", c.frame, err, buf.Len())
		}
		typ, got, err := ReadFrame(&buf)
		if err != nil || typ != FrameData || len(got) != len(payload) {
			t.Fatalf("frame %d read back: %d %d %v", c.frame, typ, len(got), err)
		}
	}
}

// A hold notice carries the refused count (spec 4.2, r25), never negative.
func TestHoldNoticeRefused(t *testing.T) {
	b, err := EncodeControl(HoldNotice{Kind: "wake", Held: 8, Active: true, Refused: 3})
	if err != nil {
		t.Fatal(err)
	}
	v, err := DecodeControl(b)
	if n, ok := v.(HoldNotice); err != nil || !ok || n.Refused != 3 {
		t.Fatalf("decoded %+v %v", v, err)
	}
	if _, err := DecodeControl([]byte(`{"op":"hold","kind":"wake","held":0,"active":true,"refused":-1}`)); err == nil {
		t.Fatal("a negative refused count decoded")
	}
}
