//go:build !aclrecord

package bus

import "github.com/redis/go-redis/v9"

// installACLRecorder is a no-op: only a build with -tags aclrecord records
// the commands each caller sends (aclrecord.go, broker-hardening step 4).
func installACLRecorder(*redis.Client) {}
