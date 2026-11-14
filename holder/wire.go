package holder

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"unicode"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The wire (spec 4.4): length-prefixed frames on a unix socket. A frame is
// a 4-byte big-endian length, then the frame's type and its payload, which
// the length counts. A control frame's payload is one JSON object; a data
// frame's is raw terminal bytes (the attach stream, spec 4.6).

// Proto is the holder protocol's version: additive changes only within it,
// as subscribe's JSON (spec 6.3). hello and welcome carry it with the build.
const Proto = 1

// Frame types.
const (
	FrameControl byte = 1
	FrameData    byte = 2
)

// MaxFrame bounds a frame (its type byte and payload): 1 MiB, the size of
// the per-terminal hold queue (spec 4.2).
const MaxFrame = 1 << 20

// ErrFrameTooLarge: a frame past MaxFrame, refused from its header alone.
var ErrFrameTooLarge = errors.New("holder: frame too large")

// WriteFrame writes one frame of type typ.
func WriteFrame(w io.Writer, typ byte, payload []byte) error {
	if len(payload)+1 > MaxFrame {
		return ErrFrameTooLarge
	}
	buf := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(buf, uint32(len(payload)+1))
	buf[4] = typ
	copy(buf[5:], payload)
	_, err := w.Write(buf)
	return err
}

// ReadFrame reads one frame: io.EOF at a clean end, an error for a frame of
// no type, of an unknown type, too large, or cut short.
func ReadFrame(r io.Reader) (byte, []byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err // io.EOF only before a header's first byte
	}
	n := binary.BigEndian.Uint32(hdr[:])
	switch {
	case n == 0:
		return 0, nil, errors.New("holder: a frame without a type")
	case n > MaxFrame:
		return 0, nil, ErrFrameTooLarge
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return 0, nil, err
	}
	if buf[0] != FrameControl && buf[0] != FrameData {
		return 0, nil, fmt.Errorf("holder: unknown frame type %d", buf[0])
	}
	return buf[0], buf[1:], nil
}

// The control frames: one struct per op, each a closed set (spec 4.2). The
// submit frame carries {kind, term, nonce, count}: no text and no names (the
// holder composes the line from its own project and the terminal's name).
// The start frame carries {role, profile?, resume_session?, size?}: no argv,
// env or cwd (security S2a; the holder builds the command from the role's
// launch template).

// Hello opens a connection (the client); Welcome answers it (the holder).
type Hello struct {
	Proto int    `json:"proto"`
	Build string `json:"build"`
}

type Welcome struct {
	Proto int    `json:"proto"`
	Build string `json:"build"`
}

type Ping struct{}

type Describe struct{}

type List struct{}

type Start struct {
	Role          string `json:"role"`
	Profile       string `json:"profile,omitempty"`
	ResumeSession string `json:"resume_session,omitempty"`
	Cols          int    `json:"cols,omitempty"`
	Rows          int    `json:"rows,omitempty"`
}

type Close struct {
	Term string `json:"term"`
}

type ScreenReq struct {
	Term  string `json:"term"`
	Lines int    `json:"lines,omitempty"`
}

type ActivityReq struct {
	Term string `json:"term"`
}

type Processes struct {
	Term string `json:"term"`
}

type Submit struct {
	Kind  string `json:"kind"`
	Term  string `json:"term"`
	Nonce string `json:"nonce,omitempty"`
	Count int    `json:"count,omitempty"`
}

type Attach struct {
	Term    string `json:"term"`
	Observe bool   `json:"observe,omitempty"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
}

// During an attach (spec 4.5, 4.6): the viewer's focus reports (control
// frames, never program input), its terminal's size, and the detach.
type Focus struct {
	In bool `json:"in"`
}

type Resize struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

type Detach struct{}

// op is each frame's name and its fields, the only keys it may carry.
var ops = map[string]struct {
	fields []string
	decode func([]byte) (any, error)
}{
	"hello":     {[]string{"proto", "build"}, decodeAs[Hello]},
	"welcome":   {[]string{"proto", "build"}, decodeAs[Welcome]},
	"ping":      {nil, decodeAs[Ping]},
	"describe":  {nil, decodeAs[Describe]},
	"list":      {nil, decodeAs[List]},
	"start":     {[]string{"role", "profile", "resume_session", "cols", "rows"}, decodeAs[Start]},
	"close":     {[]string{"term"}, decodeAs[Close]},
	"screen":    {[]string{"term", "lines"}, decodeAs[ScreenReq]},
	"activity":  {[]string{"term"}, decodeAs[ActivityReq]},
	"processes": {[]string{"term"}, decodeAs[Processes]},
	"submit":    {[]string{"kind", "term", "nonce", "count"}, decodeAs[Submit]},
	"attach":    {[]string{"term", "observe", "cols", "rows"}, decodeAs[Attach]},
	"focus":     {[]string{"in"}, decodeAs[Focus]},
	"resize":    {[]string{"cols", "rows"}, decodeAs[Resize]},
	"detach":    {nil, decodeAs[Detach]},
}

func opOf(v any) (string, bool) {
	switch v.(type) {
	case Hello:
		return "hello", true
	case Welcome:
		return "welcome", true
	case Ping:
		return "ping", true
	case Describe:
		return "describe", true
	case List:
		return "list", true
	case Start:
		return "start", true
	case Close:
		return "close", true
	case ScreenReq:
		return "screen", true
	case ActivityReq:
		return "activity", true
	case Processes:
		return "processes", true
	case Submit:
		return "submit", true
	case Attach:
		return "attach", true
	case Focus:
		return "focus", true
	case Resize:
		return "resize", true
	case Detach:
		return "detach", true
	}
	return "", false
}

func decodeAs[T any](raw []byte) (any, error) {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// EncodeControl is a control frame's payload for v, one of the frame
// structs: {"op":…} first, then its fields.
func EncodeControl(v any) ([]byte, error) {
	op, ok := opOf(v)
	if !ok {
		return nil, fmt.Errorf("holder: %T is no control frame", v)
	}
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := []byte(`{"op":"` + op + `"`)
	if rest := bytes.TrimPrefix(body, []byte("{")); len(rest) > 1 {
		out = append(out, ',')
		out = append(out, rest...)
	} else {
		out = append(out, '}')
	}
	return out, nil
}

// DecodeControl reads a control frame's payload: one JSON object, nothing
// after it, every key exactly one of its op's (no duplicate, no case
// folding: encoding/json alone would take "Kind" for "kind"), every value
// checked (validate). It returns the frame's struct.
func DecodeControl(raw []byte) (any, error) {
	keys, err := objectKeys(raw)
	if err != nil {
		return nil, err
	}
	opRaw, ok := keys["op"]
	if !ok {
		return nil, errors.New("holder: a control frame without an op")
	}
	var op string
	if err := json.Unmarshal(opRaw, &op); err != nil {
		return nil, errors.New("holder: op is not a string")
	}
	spec, ok := ops[op]
	if !ok {
		return nil, fmt.Errorf("holder: unknown op %q", op)
	}
	allowed := map[string]bool{"op": true}
	for _, f := range spec.fields {
		allowed[f] = true
	}
	for k := range keys {
		if !allowed[k] {
			return nil, fmt.Errorf("holder: %s takes no field %q", op, k)
		}
	}
	v, err := spec.decode(raw)
	if err != nil {
		return nil, fmt.Errorf("holder: %s: %w", op, err)
	}
	if err := validate(v); err != nil {
		return nil, err
	}
	return v, nil
}

// objectKeys reads raw as one JSON object, refusing anything else, a
// duplicate key, or data after it.
func objectKeys(raw []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("holder: a control frame is one JSON object")
	}
	keys := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, ok := tok.(string)
		if !ok {
			return nil, errors.New("holder: a control frame's key is not a string")
		}
		if _, dup := keys[k]; dup {
			return nil, fmt.Errorf("holder: duplicate key %q", k)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		keys[k] = v
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, errors.New("holder: a control frame is one JSON object")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("holder: data after a control frame's object")
	}
	return keys, nil
}

var (
	termRE  = regexp.MustCompile(`^t[1-9][0-9]{0,8}$`)
	nonceRE = regexp.MustCompile(`^[0-9a-f]{6,16}$`)
)

// The bounds of a size and of a screen read.
const (
	maxCols  = 1000
	maxRows  = 500
	maxLines = 100000
)

func validSize(cols, rows int) error {
	if cols == 0 && rows == 0 {
		return nil
	}
	if cols < 2 || cols > maxCols || rows < 2 || rows > maxRows {
		return fmt.Errorf("holder: size %dx%d (want both 2-%d x 2-%d, or neither)", cols, rows, maxCols, maxRows)
	}
	return nil
}

func validTerm(t string) error {
	if !termRE.MatchString(t) {
		return fmt.Errorf("holder: invalid terminal id %q", t)
	}
	return nil
}

func validBuild(b string) error {
	if b == "" || len(b) > 200 {
		return errors.New("holder: a build id of 1-200 characters")
	}
	for _, r := range b {
		if !unicode.IsPrint(r) || r > unicode.MaxASCII {
			return errors.New("holder: a build id of printable ASCII")
		}
	}
	return nil
}

// validate checks a decoded frame's values (spec 4.2's closed sets).
func validate(v any) error {
	switch f := v.(type) {
	case Hello:
		if f.Proto < 1 {
			return errors.New("holder: hello without a protocol version")
		}
		return validBuild(f.Build)
	case Welcome:
		if f.Proto < 1 {
			return errors.New("holder: welcome without a protocol version")
		}
		return validBuild(f.Build)
	case Start:
		switch {
		case !bus.ValidName(f.Role):
			return fmt.Errorf("holder: invalid role %q", f.Role)
		case f.Profile != "" && !bus.ValidName(f.Profile):
			return fmt.Errorf("holder: invalid profile %q", f.Profile)
		case f.ResumeSession != "" && !bus.ValidSessionID(f.ResumeSession):
			return errors.New("holder: invalid resume session")
		}
		return validSize(f.Cols, f.Rows)
	case Close:
		return validTerm(f.Term)
	case ScreenReq:
		if f.Lines < 0 || f.Lines > maxLines {
			return fmt.Errorf("holder: screen lines %d (0-%d)", f.Lines, maxLines)
		}
		return validTerm(f.Term)
	case ActivityReq:
		return validTerm(f.Term)
	case Processes:
		return validTerm(f.Term)
	case Attach:
		if err := validTerm(f.Term); err != nil {
			return err
		}
		return validSize(f.Cols, f.Rows)
	case Resize:
		if f.Cols == 0 && f.Rows == 0 {
			return errors.New("holder: a resize without a size")
		}
		return validSize(f.Cols, f.Rows)
	case Submit:
		if err := validTerm(f.Term); err != nil {
			return err
		}
		switch f.Kind {
		case "wake":
			if !nonceRE.MatchString(f.Nonce) {
				return errors.New("holder: a wake's nonce must match ^[0-9a-f]{6,16}$")
			}
			if f.Count < 1 || f.Count > 999 {
				return fmt.Errorf("holder: a wake's count %d (1-999)", f.Count)
			}
		case "clear":
			if f.Nonce != "" || f.Count != 0 {
				return errors.New("holder: a clear carries no nonce and no count")
			}
		default:
			return fmt.Errorf("holder: unknown submit kind %q", f.Kind)
		}
	}
	return nil
}
