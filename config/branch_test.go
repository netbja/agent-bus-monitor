package config

import (
	"strings"
	"testing"
)

// delivery.base is a Git branch name (git check-ref-format --branch's rules).
func TestValidBranchName(t *testing.T) {
	for _, ok := range []string{"main", "release/v1", "dev-2", "feat/x_y"} {
		if !ValidBranchName(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "-main", "a..b", "a b", "a~b", "a^b", "a:b", "a?b", "a*b", "a[b", "a\\b", "a/", "/a", "a.lock", "a//b", "a@{b", "@", "a.", ".a", "a/.b", "a\x01", "HEAD"} {
		if ValidBranchName(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

// broker.image accepts an image reference, and nothing compose or a shell
// would read as more (broker-hardening step 3).
func TestIsImageRef(t *testing.T) {
	digest := "@sha256:" + strings.Repeat("ab12", 16) // reviewer-codex: an image pinned by its digest, with or without a tag
	for _, ok := range []string{"redis:8-alpine", "valkey/valkey:8", "redis", "docker.io/library/redis:8.2.1", "registry.local:5000/team/valkey:8.0",
		"registry.example/team/redis" + digest, "redis:8-alpine" + digest} {
		if !IsImageRef(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "Redis:8", "redis:8 alpine", "redis;rm -rf /", "${X}", "redis:8\n", "-redis", "redis:", "../redis",
		"redis@sha256:abc", "redis@sha256:" + strings.Repeat("Z", 64), "redis@md5:" + strings.Repeat("a", 32), "redis" + digest + "x", "@sha256:" + strings.Repeat("a", 64)} {
		if IsImageRef(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
