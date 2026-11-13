package holder

import "golang.org/x/sys/unix"

func peerOfFD(fd int) (Peer, error) {
	pid, err := unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	if err != nil {
		return Peer{}, err
	}
	cred, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return Peer{}, err
	}
	return Peer{PID: pid, UID: int(cred.Uid)}, nil
}
