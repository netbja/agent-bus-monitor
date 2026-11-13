//go:build !linux && !darwin

package holder

import "errors"

func peerOfFD(int) (Peer, error) {
	return Peer{}, errors.New("holder: no peer credentials on this platform")
}
