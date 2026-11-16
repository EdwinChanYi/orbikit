package release

import (
	"errors"
	"regexp"
	"strings"
)

const (
	// RepoURL is the only repository a release may fetch.
	RepoURL = "https://github.com/EdwinChanYi/orbikit.git"
	// LogLimit is the maximum output stored on one task.
	LogLimit = 256 * 1024
)

const (
	KindPublish  = "publish"
	KindRollback = "rollback"
	KindRecreate = "recreate"
)

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

const (
	RefTag = "tag"
	RefSHA = "sha"
)

var (
	shaRef  = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	hexOnly = regexp.MustCompile(`^[0-9a-fA-F]+$`)
	tagRef  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	project = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
)

// ErrInvalidRef is returned for a branch name, a short SHA, or any other ref.
var ErrInvalidRef = errors.New("引用无效")

// ParseRef accepts a tag or a full 40-character SHA.
// A shorter all-hex string is a short SHA and is rejected.
func ParseRef(ref string) (string, error) {
	if shaRef.MatchString(ref) {
		return RefSHA, nil
	}
	if hexOnly.MatchString(ref) || !tagRef.MatchString(ref) {
		return "", ErrInvalidRef
	}
	return RefTag, nil
}

// Allowed reports whether service may be published.
func Allowed(service string) bool {
	switch service {
	case "core", "chatroom", "gateway", "webhook", "admin", "builder":
		return true
	default:
		return false
	}
}

// ContainerName returns the compose container for one release service.
func ContainerName(projectName, service string) (string, error) {
	if !project.MatchString(projectName) || !Allowed(service) {
		return "", errors.New("服务名不在允许范围内")
	}
	return projectName + "-" + service + "-1", nil
}

// ImageTag is the local docker tag for one built ref. Docker tags are lowercase.
func ImageTag(service, ref string) string {
	return "orbikit-" + service + ":" + strings.ToLower(ref)
}

// Tail keeps the last LogLimit bytes of output.
func Tail(text string) string {
	if len(text) <= LogLimit {
		return text
	}
	return text[len(text)-LogLimit:]
}
