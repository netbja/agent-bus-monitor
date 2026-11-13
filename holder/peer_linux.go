package holder

import "golang.org/x/sys/unix"

func peerOfFD(fd int) (Peer, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return Peer{}, err
	}
	return Peer{PID: int(cred.Pid), UID: int(cred.Uid)}, nil
}
