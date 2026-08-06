// internal/cmd/sandbox/redact.go
package sandbox

import "regexp"

// tokenParamRe matches a token query parameter and its value. Both lane URLs
// carry the JWT that way, and a failed dial reports the URL it tried — so the
// raw token would otherwise reach the log panel and retask.log.
var tokenParamRe = regexp.MustCompile(`([?&]token=)[^&"\s]*`)

// redactedToken replaces the token value in log output.
const redactedToken = "REDACTED"

// redactToken blanks the value of every token query parameter in s.
func redactToken(s string) string {
	return tokenParamRe.ReplaceAllString(s, "${1}"+redactedToken)
}

// redactErr returns err with any token query parameter in its message redacted.
// The original error stays reachable through Unwrap, so errors.Is and errors.As
// keep working on the wrapped chain.
func redactErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	redacted := redactToken(msg)
	if redacted == msg {
		return err
	}
	return &redactedError{msg: redacted, err: err}
}

// redactedError presents a scrubbed message while preserving the error chain.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }
