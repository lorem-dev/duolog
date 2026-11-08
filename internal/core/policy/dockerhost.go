package policy

import (
	"regexp"
	"strings"
)

var (
	// dockerSocketSegment is one folder or file name of a socket path: plain
	// text, with no ~, $, quote, glob or separator the shell could act on.
	dockerSocketSegment = regexp.MustCompile(`^[A-Za-z0-9._@+-]+$`)
	// dockerLocalTCP is a daemon on this machine over tcp.
	dockerLocalTCP = regexp.MustCompile(`^(?:localhost|127\.0\.0\.1|\[::1\])(?::[0-9]{1,5})?/?$`)
)

// localDockerHost reports whether an assignment only points a Docker client
// at a daemon on this machine: a unix socket at an absolute path, or tcp to
// localhost, 127.0.0.1 or [::1]. The client connects to the socket; it does
// not read or write it as a file, so the path may lie outside the work root.
// A path with . or .. as a name, or with a character the shell could expand,
// does not count.
func localDockerHost(word string) bool {
	value, ok := strings.CutPrefix(word, "DOCKER_HOST=")
	if !ok {
		return false
	}
	if address, ok := strings.CutPrefix(value, "tcp://"); ok {
		return dockerLocalTCP.MatchString(address)
	}
	socket, ok := strings.CutPrefix(value, "unix:///")
	if !ok {
		return false
	}
	for _, segment := range strings.Split(socket, "/") {
		if segment == "." || segment == ".." || !dockerSocketSegment.MatchString(segment) {
			return false
		}
	}
	return true
}

// dropLocalDockerHost returns the words after the local DOCKER_HOST
// assignments in front of a command, so the gate judges the command that
// follows as if it stood alone. A part that is only such assignments stays
// whole, as do assignments to any other name, which keep their ask.
func dropLocalDockerHost(words []string) []string {
	i := 0
	for i < len(words) && localDockerHost(words[i]) {
		i++
	}
	if i == len(words) {
		return words
	}
	return words[i:]
}
