//go:build !unix

package loginexpiry

import "os"

// ownedByMe: no owner to compare outside unix (Windows is out of the
// project): never owned, so the file is never read.
func ownedByMe(os.FileInfo) bool { return false }
